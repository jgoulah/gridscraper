package scraper

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/jgoulah/gridscraper/internal/config"
	"github.com/jgoulah/gridscraper/pkg/models"
)

const (
	conedLoginURL  = "https://www.coned.com/en/login"
	conedGraphQLURL = "https://cned.opower.com/ei/edge/apis/dsm-graphql-v1/cws/graphql"
)

// ConEdScraper scrapes data from Con Edison using direct API calls
type ConEdScraper struct {
	cookies         []config.Cookie
	username        string
	password        string
	challengeAnswer string
	visible         bool
	bearerToken     string
	customerUUID    string
}

// NewConEdScraper creates a new Con Edison scraper
func NewConEdScraper(cookies []config.Cookie) *ConEdScraper {
	return &ConEdScraper{cookies: cookies}
}

// NewConEdScraperWithCredentials creates a new Con Edison scraper with credentials for auto-login
func NewConEdScraperWithCredentials(cookies []config.Cookie, authToken, customerUUID, username, password, challengeAnswer string) *ConEdScraper {
	return &ConEdScraper{
		cookies:         cookies,
		bearerToken:     authToken,
		customerUUID:    customerUUID,
		username:        username,
		password:        password,
		challengeAnswer: challengeAnswer,
	}
}

// SetVisible sets whether to show the browser window
func (s *ConEdScraper) SetVisible(visible bool) {
	s.visible = visible
}

// Scrape fetches usage data from Con Edison
func (s *ConEdScraper) Scrape(ctx context.Context, daysToFetch int) ([]models.UsageData, error) {
	// Get Bearer token and customer UUID via login
	if err := s.authenticate(ctx); err != nil {
		return nil, fmt.Errorf("authentication failed: %w", err)
	}

	// Generate export job via GraphQL API
	jobUUID, err := s.createExportJob(ctx, daysToFetch)
	if err != nil {
		return nil, fmt.Errorf("creating export job: %w", err)
	}

	// Poll until job completes and get download URL
	downloadURL, err := s.pollJobStatus(ctx, jobUUID)
	if err != nil {
		return nil, fmt.Errorf("polling job status: %w", err)
	}

	// Download ZIP file
	zipPath, err := s.downloadZIP(ctx, downloadURL)
	if err != nil {
		return nil, fmt.Errorf("downloading ZIP: %w", err)
	}
	defer os.Remove(zipPath)

	// Extract CSV from ZIP
	csvPath, err := extractCSVFromZip(zipPath)
	if err != nil {
		return nil, fmt.Errorf("extracting CSV: %w", err)
	}
	defer os.Remove(csvPath)

	// Parse CSV data
	data, err := s.parseCSV(csvPath)
	if err != nil {
		return nil, fmt.Errorf("parsing CSV: %w", err)
	}

	return data, nil
}

// authenticate logs in and extracts the Bearer token and customer UUID
func (s *ConEdScraper) authenticate(ctx context.Context) error {
	// If we already have token and UUID from RefreshAuth(), skip login
	if s.bearerToken != "" && s.customerUUID != "" {
		fmt.Println("  Using existing authentication from RefreshAuth()")
		return nil
	}
	
	fmt.Println("Authenticating to Con Edison...")

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", !s.visible),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(ctx, opts...)
	defer cancel()

	browserCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	browserCtx, cancel = context.WithTimeout(browserCtx, 30*time.Second)
	defer cancel()

	// Navigate to login page
	if err := chromedp.Run(browserCtx, chromedp.Navigate(conedLoginURL)); err != nil {
		return fmt.Errorf("navigating to login: %w", err)
	}

	// Fill login form
	fmt.Println("  Filling login form...")
	if err := chromedp.Run(browserCtx,
		chromedp.Sleep(2*time.Second), // Wait for page to fully load
		chromedp.WaitVisible(`input#form-login-email`, chromedp.ByQuery),
		chromedp.SendKeys(`input#form-login-email`, s.username, chromedp.ByQuery),
		chromedp.SendKeys(`input#form-login-password`, s.password, chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond), // Short wait for validation
	); err != nil {
		return fmt.Errorf("filling login form: %w", err)
	}

	// Submit login form
	fmt.Println("  Submitting login form...")
	if err := chromedp.Run(browserCtx,
		chromedp.Sleep(1*time.Second), // Wait for button to be enabled
		chromedp.WaitVisible(`button.js-login-submit-button`, chromedp.ByQuery),
		chromedp.Click(`button.js-login-submit-button`, chromedp.ByQuery),
		chromedp.Sleep(5*time.Second), // Wait for login to process
	); err != nil {
		return fmt.Errorf("submitting login form: %w", err)
	}

	// Handle challenge question if present
	var challengeVisible bool
	chromedp.Run(browserCtx,
		chromedp.Sleep(2*time.Second), // Give page time to show challenge
		chromedp.Evaluate(`document.querySelector('input#form-login-mfa-code') !== null`, &challengeVisible),
	)

	if challengeVisible {
		fmt.Println("  Answering challenge question...")
		if s.challengeAnswer == "" {
			return fmt.Errorf("challenge question required but no answer configured")
		}

		if err := chromedp.Run(browserCtx,
			chromedp.WaitVisible(`input#form-login-mfa-code`, chromedp.ByQuery),
			chromedp.SendKeys(`input#form-login-mfa-code`, s.challengeAnswer, chromedp.ByQuery),
			chromedp.Sleep(1*time.Second), // Wait for button to be enabled
			chromedp.WaitVisible(`button.js-device-submit-button`, chromedp.ByQuery),
			chromedp.Click(`button.js-device-submit-button`, chromedp.ByQuery),
			chromedp.Sleep(8*time.Second), // Wait longer for login to complete
		); err != nil {
			return fmt.Errorf("answering challenge question: %w", err)
		}
	} else {
		// No challenge, just wait for login to complete
		chromedp.Run(browserCtx, chromedp.Sleep(5*time.Second))
	}

	// Check current URL after login
	var currentURL string
	chromedp.Run(browserCtx, chromedp.Evaluate(`window.location.href`, &currentURL))
	fmt.Printf("  Current URL after login: %s\n", currentURL)

	// Navigate to energy usage page where the token is available
	fmt.Println("  Navigating to energy usage page...")
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate("https://www.coned.com/en/accounts-billing/my-account/energy-use"),
		chromedp.Sleep(5*time.Second), // Wait for page to load
	); err != nil {
		return fmt.Errorf("navigating to energy page: %w", err)
	}

	// Get Bearer token from Con Edison API using chromedp to make the request
	// This ensures all cookies (including HTTP-only) are included
	fmt.Println("  Fetching Bearer token...")
	var tokenResponse string
	tokenURL := "https://www.coned.com/sitecore/api/ssc/ConEd-Cms-Services-Controllers-Opower/OpowerService/0/GetOPowerToken"

	if err := chromedp.Run(browserCtx,
		chromedp.Evaluate(fmt.Sprintf(`
			(() => {
				const xhr = new XMLHttpRequest();
				xhr.open('GET', '%s', false); // synchronous
				try {
					xhr.send();
					if (xhr.status === 200) {
						return xhr.responseText;
					} else {
						return 'ERROR: HTTP ' + xhr.status;
					}
				} catch (e) {
					return 'ERROR: ' + e.toString();
				}
			})()
		`, tokenURL), &tokenResponse),
	); err != nil {
		return fmt.Errorf("fetching token via XHR: %w", err)
	}

	fmt.Printf("  Token response: %q (length: %d)\n", tokenResponse, len(tokenResponse))

	if strings.HasPrefix(tokenResponse, "ERROR:") {
		return fmt.Errorf("token fetch failed: %s", tokenResponse)
	}

	if tokenResponse == "" || tokenResponse == "null" || len(tokenResponse) < 20 {
		return fmt.Errorf("token response invalid: %q", tokenResponse)
	}

	// The response is a JSON string with quotes
	if err := json.Unmarshal([]byte(tokenResponse), &s.bearerToken); err != nil {
		// Maybe it's already unquoted?
		s.bearerToken = tokenResponse
	}

	if s.bearerToken == "" || s.bearerToken == "null" {
		return fmt.Errorf("received empty Bearer token")
	}

	// Get customer UUID using Bearer token via fetch
	fmt.Println("  Fetching customer UUID...")
	var customerResponse string
	customerURL := "https://cned.opower.com/ei/edge/apis/multi-account-v1/cws/cned/customers/current"

	if err := chromedp.Run(browserCtx,
		chromedp.Evaluate(fmt.Sprintf(`
			(() => {
				const xhr = new XMLHttpRequest();
				xhr.open('GET', '%s', false); // synchronous
				xhr.setRequestHeader('Authorization', 'Bearer %s');
				xhr.setRequestHeader('Accept', 'application/json');
				try {
					xhr.send();
					if (xhr.status === 200) {
						return xhr.responseText;
					} else {
						return 'ERROR: HTTP ' + xhr.status;
					}
				} catch (e) {
					return 'ERROR: ' + e.toString();
				}
			})()
		`, customerURL, s.bearerToken), &customerResponse),
	); err != nil {
		return fmt.Errorf("fetching customer UUID via XHR: %w", err)
	}

	if strings.HasPrefix(customerResponse, "ERROR:") {
		return fmt.Errorf("customer fetch failed: %s", customerResponse)
	}

	var customerData struct {
		UUID string `json:"uuid"`
	}
	if err := json.Unmarshal([]byte(customerResponse), &customerData); err != nil {
		return fmt.Errorf("parsing customer data: %w", err)
	}

	s.customerUUID = customerData.UUID
	if s.customerUUID == "" {
		return fmt.Errorf("received empty customer UUID")
	}

	fmt.Printf("✓ Authentication successful (Customer: %s)\n", s.customerUUID)
	return nil
}

// createExportJob creates a new export job via GraphQL API
func (s *ConEdScraper) createExportJob(ctx context.Context, daysToFetch int) (string, error) {
	fmt.Println("Creating export job...")

	// Calculate time interval
	toDate := time.Now()
	fromDate := toDate.AddDate(0, 0, -daysToFetch)

	// Format as ISO 8601 with timezone
	_, offsetSeconds := toDate.Zone()
	offsetHours := offsetSeconds / 3600
	sign := "+"
	if offsetHours < 0 {
		sign = "-"
		offsetHours = -offsetHours
	}
	tzOffset := fmt.Sprintf("%s%02d:00", sign, offsetHours)
	timeInterval := fmt.Sprintf("%s/%s",
		fromDate.Format("2006-01-02T15:04:05")+tzOffset,
		toDate.Format("2006-01-02T15:04:05")+tzOffset,
	)

	payload := map[string]interface{}{
		"operationName": "WUE_GenerateUsageExportFile",
		"variables": map[string]interface{}{
			"usageExportFileConfigurationInput": map[string]interface{}{
				"customerUuid":       s.customerUUID,
				"utilityCode":        "cned",
				"forceLegacyData":    true,
				"maxAgeOfDataInDays": 1095,
				"format":             "CSV",
				"timeInterval":       timeInterval,
				"messages":           s.getExportMessages(),
				"unitsOfMeasureAllowed":                  []string{},
				"utilityServiceQuantityIdentifiersAllowed": []string{},
				"displayNameStrategy":                      "UTILITY_ACCOUNT_NICKNAME_AS_DISPLAY_NAME_STRATEGY",
				"showServicePoint":                         false,
				"showDevice":                               false,
				"enableServiceAgreementAliasing":           false,
				"enableFinerResolutions":                   false,
				"fileUtilityCode":                          "",
				"hideIntervalCosts":                        false,
				"showOnlyNetUsage":                         false,
			},
			"locale": "en-US",
		},
		"query": `mutation WUE_GenerateUsageExportFile($usageExportFileConfigurationInput: UsageExportFileConfigurationInput) {
  generateUsageExportFile(
    usageExportFileConfigurationInput: $usageExportFileConfigurationInput
  ) {
    uuid
    __typename
  }
}`,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", conedGraphQLURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.bearerToken)
	req.Header.Set("opower-selected-entities", fmt.Sprintf(`["urn:opower:customer:uuid:%s"]`, s.customerUUID))
	req.Header.Set("Origin", "https://www.coned.com")
	req.Header.Set("Referer", "https://www.coned.com/")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data struct {
			GenerateUsageExportFile struct {
				UUID string `json:"uuid"`
			} `json:"generateUsageExportFile"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}

	fmt.Printf("✓ Export job created: %s\n", result.Data.GenerateUsageExportFile.UUID)
	return result.Data.GenerateUsageExportFile.UUID, nil
}

// pollJobStatus polls the job status until it completes
func (s *ConEdScraper) pollJobStatus(ctx context.Context, jobUUID string) (string, error) {
	fmt.Println("Waiting for export job to complete...")

	maxAttempts := 60 // 60 seconds max
	for i := 0; i < maxAttempts; i++ {
		time.Sleep(1 * time.Second)

		payload := map[string]interface{}{
			"operationName": "WUE_GetExportJob",
			"variables": map[string]interface{}{
				"jobUuid":       jobUUID,
				"customerURN":   fmt.Sprintf("urn:opower:customer:uuid:%s", s.customerUUID),
				"forceLegacyData": true,
				"locale":        "en-US",
			},
			"query": `query WUE_GetExportJob($jobUuid: ID!) {
  exportJob(jobUuid: $jobUuid) {
    uuid
    result
    isRunning
    isFailed
    isFinished
    __typename
  }
}`,
		}

		jsonData, err := json.Marshal(payload)
		if err != nil {
			return "", fmt.Errorf("marshaling request: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, "POST", conedGraphQLURL, bytes.NewBuffer(jsonData))
		if err != nil {
			return "", fmt.Errorf("creating request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+s.bearerToken)
		req.Header.Set("opower-selected-entities", fmt.Sprintf(`["urn:opower:customer:uuid:%s"]`, s.customerUUID))
		req.Header.Set("Origin", "https://www.coned.com")
		req.Header.Set("Referer", "https://www.coned.com/")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", fmt.Errorf("sending request: %w", err)
		}

		var result struct {
			Data struct {
				ExportJob struct {
					UUID       string  `json:"uuid"`
					Result     *string `json:"result"`
					IsRunning  bool    `json:"isRunning"`
					IsFailed   *bool   `json:"isFailed"`
					IsFinished *bool   `json:"isFinished"`
				} `json:"exportJob"`
			} `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if err := json.Unmarshal(body, &result); err != nil {
			return "", fmt.Errorf("decoding response: %w (body: %s)", err, string(body))
		}

		job := result.Data.ExportJob

		if job.IsFailed != nil && *job.IsFailed {
			// Log the full response for debugging
			fmt.Printf("  Job failed - full response: %s\n", string(body))
			if len(result.Errors) > 0 {
				return "", fmt.Errorf("export job failed: %s", result.Errors[0].Message)
			}
			return "", fmt.Errorf("export job failed (no error details provided)")
		}

		if job.IsFinished != nil && *job.IsFinished && job.Result != nil {
			fmt.Printf("✓ Export job completed after %d seconds\n", i+1)
			return *job.Result, nil
		}

		if i%5 == 0 {
			fmt.Printf("  Still waiting... (%d seconds)\n", i+1)
		}
	}

	return "", fmt.Errorf("export job timed out after %d seconds", maxAttempts)
}

// downloadZIP downloads the ZIP file from the given URL
func (s *ConEdScraper) downloadZIP(ctx context.Context, downloadURL string) (string, error) {
	fmt.Println("Downloading ZIP file...")

	req, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("downloading file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	// Create temp file
	tmpFile, err := os.CreateTemp("", "coned-export-*.zip")
	if err != nil {
		return "", fmt.Errorf("creating temp file: %w", err)
	}
	defer tmpFile.Close()

	// Copy response to file
	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("writing file: %w", err)
	}

	fmt.Printf("✓ Downloaded to %s\n", tmpFile.Name())
	return tmpFile.Name(), nil
}

// RefreshAuth performs a fresh login and returns new cookies, bearer token, and customer UUID
func (s *ConEdScraper) RefreshAuth(ctx context.Context) ([]config.Cookie, string, string, error) {
	fmt.Println("Refreshing authentication...")

	if s.username == "" || s.password == "" {
		return nil, "", "", fmt.Errorf("username and password required for refresh")
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", !s.visible),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(ctx, opts...)
	defer cancel()

	browserCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	browserCtx, cancel = context.WithTimeout(browserCtx, 60*time.Second)
	defer cancel()

	// Navigate and login
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(conedLoginURL),
		chromedp.Sleep(2*time.Second), // Wait for page to load
		chromedp.WaitVisible(`input#form-login-email`, chromedp.ByQuery),
		chromedp.SendKeys(`input#form-login-email`, s.username, chromedp.ByQuery),
		chromedp.SendKeys(`input#form-login-password`, s.password, chromedp.ByQuery),
		chromedp.Sleep(1*time.Second), // Wait for validation
		chromedp.WaitVisible(`button.js-login-submit-button`, chromedp.ByQuery),
		chromedp.Click(`button.js-login-submit-button`, chromedp.ByQuery),
		chromedp.Sleep(5*time.Second), // Wait for navigation
	); err != nil {
		return nil, "", "", fmt.Errorf("login failed: %w", err)
	}

	// Handle challenge question
	var challengeVisible bool
	chromedp.Run(browserCtx,
		chromedp.Sleep(2*time.Second), // Give page time to show challenge
		chromedp.Evaluate(`document.querySelector('input#form-login-mfa-code') !== null`, &challengeVisible),
	)

	if challengeVisible {
		fmt.Println("Challenge question detected, answering...")
		if s.challengeAnswer == "" {
			return nil, "", "", fmt.Errorf("challenge question required but no answer configured")
		}

		if err := chromedp.Run(browserCtx,
			chromedp.WaitVisible(`input#form-login-mfa-code`, chromedp.ByQuery),
			chromedp.SendKeys(`input#form-login-mfa-code`, s.challengeAnswer, chromedp.ByQuery),
			chromedp.Sleep(1*time.Second), // Wait for validation
			chromedp.WaitVisible(`button.js-device-submit-button`, chromedp.ByQuery),
			chromedp.Click(`button.js-device-submit-button`, chromedp.ByQuery),
			chromedp.Sleep(5*time.Second),
		); err != nil {
			return nil, "", "", fmt.Errorf("answering challenge question failed: %w", err)
		}
	}

	// Extract Bearer token and customer UUID for future use
	// Navigate to energy usage page where the token is available
	fmt.Println("  Navigating to energy usage page...")
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate("https://www.coned.com/en/accounts-billing/my-account/energy-use"),
		chromedp.Sleep(5*time.Second), // Wait for page to load
	); err != nil {
		return nil, "", "", fmt.Errorf("navigating to energy page: %w", err)
	}

	fmt.Println("  Extracting authentication tokens...")

	// Get Bearer token
	var tokenResponse string
	tokenURL := "https://www.coned.com/sitecore/api/ssc/ConEd-Cms-Services-Controllers-Opower/OpowerService/0/GetOPowerToken"

	if err := chromedp.Run(browserCtx,
		chromedp.Evaluate(fmt.Sprintf(`
			(() => {
				const xhr = new XMLHttpRequest();
				xhr.open('GET', '%s', false); // synchronous
				try {
					xhr.send();
					if (xhr.status === 200) {
						return xhr.responseText;
					} else {
						return 'ERROR: HTTP ' + xhr.status;
					}
				} catch (e) {
					return 'ERROR: ' + e.toString();
				}
			})()
		`, tokenURL), &tokenResponse),
	); err != nil {
		return nil, "", "", fmt.Errorf("fetching token via XHR: %w", err)
	}

	if strings.HasPrefix(tokenResponse, "ERROR:") {
		return nil, "", "", fmt.Errorf("token fetch failed: %s", tokenResponse)
	}

	if tokenResponse == "" || tokenResponse == "null" || len(tokenResponse) < 20 {
		return nil, "", "", fmt.Errorf("token response invalid: %q", tokenResponse)
	}

	// The response is a JSON string with quotes
	if err := json.Unmarshal([]byte(tokenResponse), &s.bearerToken); err != nil {
		// Maybe it's already unquoted?
		s.bearerToken = tokenResponse
	}

	if s.bearerToken == "" || s.bearerToken == "null" {
		return nil, "", "", fmt.Errorf("received empty Bearer token")
	}

	// Get customer UUID
	var customerResponse string
	customerURL := "https://cned.opower.com/ei/edge/apis/multi-account-v1/cws/cned/customers/current"

	if err := chromedp.Run(browserCtx,
		chromedp.Evaluate(fmt.Sprintf(`
			(() => {
				const xhr = new XMLHttpRequest();
				xhr.open('GET', '%s', false); // synchronous
				xhr.setRequestHeader('Authorization', 'Bearer %s');
				xhr.setRequestHeader('Accept', 'application/json');
				try {
					xhr.send();
					if (xhr.status === 200) {
						return xhr.responseText;
					} else {
						return 'ERROR: HTTP ' + xhr.status;
					}
				} catch (e) {
					return 'ERROR: ' + e.toString();
				}
			})()
		`, customerURL, s.bearerToken), &customerResponse),
	); err != nil {
		return nil, "", "", fmt.Errorf("fetching customer UUID via XHR: %w", err)
	}

	if strings.HasPrefix(customerResponse, "ERROR:") {
		return nil, "", "", fmt.Errorf("customer fetch failed: %s", customerResponse)
	}

	var customerData struct {
		UUID string `json:"uuid"`
	}
	if err := json.Unmarshal([]byte(customerResponse), &customerData); err != nil {
		return nil, "", "", fmt.Errorf("parsing customer data: %w", err)
	}

	s.customerUUID = customerData.UUID
	if s.customerUUID == "" {
		return nil, "", "", fmt.Errorf("received empty customer UUID")
	}

	fmt.Printf("✓ Authentication refreshed successfully (Customer: %s)\n", s.customerUUID)
	return []config.Cookie{}, s.bearerToken, s.customerUUID, nil
}

// parseCSV parses the CSV data and aggregates to hourly readings
func (s *ConEdScraper) parseCSV(csvPath string) ([]models.UsageData, error) {
	fmt.Println("Parsing CSV data...")

	file, err := os.Open(csvPath)
	if err != nil {
		return nil, fmt.Errorf("opening CSV: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1 // Allow variable number of fields
	reader.TrimLeadingSpace = true

	// The CSV format is: TYPE, DATE, START_TIME, END_TIME, USAGE
	// No header row - data starts immediately
	// Column indices are fixed:
	dateIdx := 1       // DATE column
	startTimeIdx := 2  // START TIME column
	usageIdx := 4      // USAGE column

	// Aggregate 15-minute readings to hourly
	hourlyData := make(map[string]float64) // "YYYY-MM-DD HH" -> sum

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading record: %w", err)
		}

		if len(record) <= usageIdx {
			continue
		}

		// Parse usage
		usageStr := strings.TrimSpace(record[usageIdx])
		if usageStr == "" {
			continue
		}

		usage, err := strconv.ParseFloat(usageStr, 64)
		if err != nil {
			continue
		}

		// Get date and time
		dateStr := strings.TrimSpace(record[dateIdx])
		startTime := strings.TrimSpace(record[startTimeIdx])

		// Parse time to get hour
		var hour string
		if len(startTime) >= 2 {
			// Assume format like "00:00", "01:00", etc.
			hour = startTime[:2]
		} else {
			continue
		}

		// Create hourly key
		hourKey := fmt.Sprintf("%s %s", dateStr, hour)
		hourlyData[hourKey] += usage
	}

	// Convert to UsageData
	var data []models.UsageData
	for hourKey, usage := range hourlyData {
		parts := strings.Split(hourKey, " ")
		if len(parts) != 2 {
			continue
		}

		dateStr := parts[0]
		hourStr := parts[1]

		// Parse date (YYYY-MM-DD format from CSV)
		t, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}

		// Parse hour
		hourInt, err := strconv.Atoi(hourStr)
		if err != nil {
			continue
		}

		// Create timestamps for the hour
		startTime := time.Date(t.Year(), t.Month(), t.Day(), hourInt, 0, 0, 0, t.Location())
		endTime := startTime.Add(1 * time.Hour)

		data = append(data, models.UsageData{
			Date:      time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()),
			StartTime: startTime,
			EndTime:   endTime,
			KWh:       usage,
			Service:   "coned",
		})
	}

	fmt.Printf("✓ Parsed %d hourly data points\n", len(data))
	return data, nil
}

// extractCSVFromZip extracts the CSV file from a ZIP archive
func extractCSVFromZip(zipPath string) (string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("opening ZIP: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		if strings.HasSuffix(f.Name, ".csv") {
			rc, err := f.Open()
			if err != nil {
				return "", fmt.Errorf("opening file in ZIP: %w", err)
			}

			tmpFile, err := os.CreateTemp("", "coned-export-*.csv")
			if err != nil {
				rc.Close()
				return "", fmt.Errorf("creating temp file: %w", err)
			}

			if _, err := io.Copy(tmpFile, rc); err != nil {
				rc.Close()
				tmpFile.Close()
				os.Remove(tmpFile.Name())
				return "", fmt.Errorf("extracting CSV: %w", err)
			}

			rc.Close()
			tmpFile.Close()
			return tmpFile.Name(), nil
		}
	}

	return "", fmt.Errorf("no CSV file found in ZIP")
}

// getExportMessages returns the message configuration for the export
func (s *ConEdScraper) getExportMessages() []map[string]string {
	return []map[string]string{
		{"key": "HEADER_TYPE", "value": "TYPE"},
		{"key": "HEADER_DATE", "value": "DATE"},
		{"key": "HEADER_USAGE", "value": "USAGE"},
		{"key": "HEADER_UNITS", "value": "UNITS"},
		{"key": "HEADER_NOTES", "value": "NOTES"},
		{"key": "HEADER_START_TIME", "value": "START TIME"},
		{"key": "HEADER_END_TIME", "value": "END TIME"},
		{"key": "LABEL_UNITS_KWH", "value": "kWh"},
	}
}

// Helper function for absolute value
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
