package scraper

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/jgoulah/gridscraper/internal/config"
	"github.com/jgoulah/gridscraper/pkg/models"
)

const nysegAPIURL = "https://engage-api-gw-dod79bsd.ue.gateway.dev/usage/usage/download"

// AuthError represents an authentication failure
type AuthError struct {
	StatusCode int
	Message    string
}

func (e *AuthError) Error() string {
	return e.Message
}

// NYSEGDirectScraper scrapes data from NYSEG using direct API calls
type NYSEGDirectScraper struct {
	cookies   []config.Cookie
	authToken string
	username  string
	password  string
}

// NewNYSEGDirectScraper creates a new NYSEG direct API scraper
func NewNYSEGDirectScraper(cookies []config.Cookie) *NYSEGDirectScraper {
	return &NYSEGDirectScraper{cookies: cookies}
}

// NewNYSEGDirectScraperWithToken creates a new NYSEG direct API scraper with auth token
func NewNYSEGDirectScraperWithToken(cookies []config.Cookie, authToken string) *NYSEGDirectScraper {
	return &NYSEGDirectScraper{
		cookies:   cookies,
		authToken: authToken,
	}
}

// NewNYSEGDirectScraperWithCredentials creates a new NYSEG direct API scraper with credentials for auto-login
func NewNYSEGDirectScraperWithCredentials(cookies []config.Cookie, authToken, username, password string) *NYSEGDirectScraper {
	return &NYSEGDirectScraper{
		cookies:   cookies,
		authToken: authToken,
		username:  username,
		password:  password,
	}
}

// RefreshAuth performs login and refreshes cookies and auth token
func (s *NYSEGDirectScraper) RefreshAuth(ctx context.Context) ([]config.Cookie, string, error) {
	if s.username == "" || s.password == "" {
		return nil, "", fmt.Errorf("cannot auto-refresh: no username/password configured")
	}

	fmt.Println("Refreshing authentication...")

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-features", "IsolateOrigins,site-per-process"),
		chromedp.Flag("disable-http2", true),
		chromedp.Flag("disable-quic", true),
		chromedp.UserAgent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(ctx, opts...)
	defer cancel()

	browserCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	browserCtx, cancel = context.WithTimeout(browserCtx, 60*time.Second)
	defer cancel()

	// Set up network monitoring to capture auth token from API requests
	var capturedAuthToken string
	var tokenCaptured bool
	chromedp.ListenTarget(browserCtx, func(ev interface{}) {
		switch ev := ev.(type) {
		case *network.EventRequestWillBeSent:
			// Capture auth token from any request (only capture once)
			if !tokenCaptured {
				if authToken, ok := ev.Request.Headers["Up-Authorization"]; ok {
					if authStr, ok := authToken.(string); ok && authStr != "" {
						capturedAuthToken = authStr
						tokenCaptured = true
					}
				}
			}
		}
	})

	// Perform login
	const loginURL = "https://sso.nyseg.com/es/login"
	if err := chromedp.Run(browserCtx,
		network.Enable(),
		chromedp.Navigate(loginURL),
		chromedp.Sleep(2*time.Second), // Wait for page to fully load
		chromedp.WaitVisible(`input#_com_liferay_login_web_portlet_LoginPortlet_login`, chromedp.ByQuery),
		chromedp.SendKeys(`input#_com_liferay_login_web_portlet_LoginPortlet_login`, s.username, chromedp.ByQuery),
		chromedp.SendKeys(`input#_com_liferay_login_web_portlet_LoginPortlet_password`, s.password, chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
		chromedp.Click(`button[type="submit"]`, chromedp.ByQuery),
		chromedp.Sleep(5*time.Second), // Wait longer for redirect and auth
	); err != nil {
		return nil, "", fmt.Errorf("login failed: %w", err)
	}

	// Navigate to insights to trigger API calls that will include the auth token
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(nysegInsightsURL),
		chromedp.Sleep(3*time.Second), // Wait for page to load
		chromedp.WaitVisible(`div.engage-insights-explore`, chromedp.ByQuery),
		chromedp.Sleep(3*time.Second), // Wait for API calls to complete
	); err != nil {
		return nil, "", fmt.Errorf("navigating to insights: %w", err)
	}

	// Extract cookies
	freshCookies, err := ExtractCookies(browserCtx)
	if err != nil {
		return nil, "", fmt.Errorf("extracting cookies: %w", err)
	}

	// Use the auth token captured from network requests
	if capturedAuthToken == "" {
		return nil, "", fmt.Errorf("could not capture auth token from network requests (did the page load correctly?)")
	}

	fmt.Println("✓ Authentication refreshed successfully")
	return freshCookies, capturedAuthToken, nil
}

// Scrape fetches usage data from NYSEG API
func (s *NYSEGDirectScraper) Scrape(ctx context.Context, daysToFetch int) ([]models.UsageData, error) {
	// If we don't have an auth token, try to get it
	if s.authToken == "" {
		if s.username != "" && s.password != "" {
			// Auto-refresh if we have credentials
			cookies, token, err := s.RefreshAuth(ctx)
			if err != nil {
				return nil, fmt.Errorf("refreshing auth: %w", err)
			}
			s.cookies = cookies
			s.authToken = token
		} else {
			// Fall back to browser extraction
			token, err := s.extractAuthTokenFromBrowser(ctx)
			if err != nil {
				return nil, fmt.Errorf("extracting auth token: %w", err)
			}
			s.authToken = token
		}
	}
	// Calculate date range
	if daysToFetch <= 0 {
		daysToFetch = 90 // Default to 90 days (3 months)
	}
	endDate := time.Now()
	startDate := endDate.AddDate(0, 0, -daysToFetch)

	// Build request URL
	params := url.Values{}
	params.Set("from_ces", "True")
	params.Set("commodity", "electric")
	params.Set("date", startDate.Format("2006-01-02"))
	params.Set("end_date", endDate.Format("2006-01-02"))
	params.Set("format", "csv")

	reqURL := fmt.Sprintf("%s?%s", nysegAPIURL, params.Encode())

	// Create HTTP client with cookies
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	// Set headers
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Referer", nysegInsightsURL)

	// Add cookies
	for _, cookie := range s.cookies {
		req.AddCookie(&http.Cookie{
			Name:     cookie.Name,
			Value:    cookie.Value,
			Domain:   cookie.Domain,
			Path:     cookie.Path,
			Expires:  time.Unix(int64(cookie.Expires), 0),
			HttpOnly: cookie.HTTPOnly,
			Secure:   cookie.Secure,
		})
	}

	// Set the Up-Authorization token
	if s.authToken != "" {
		req.Header.Set("Up-Authorization", s.authToken)
	}

	fmt.Printf("Making API request to: %s\n", reqURL)

	// Make the request
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("making request: %w", err)
	}
	defer resp.Body.Close()

	// Check for authentication errors
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		return nil, &AuthError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("authentication failed (status %d): %s", resp.StatusCode, string(body)),
		}
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Check content type
	contentType := resp.Header.Get("Content-Type")
	fmt.Printf("Response content-type: %s\n", contentType)

	// Read response body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	// Check if response is JSON with promise_id (async API)
	if contentType == "application/json" {
		var promiseResp struct {
			PromiseID string `json:"promise_id"`
		}
		if err := json.Unmarshal(bodyBytes, &promiseResp); err == nil && promiseResp.PromiseID != "" {
			fmt.Printf("Got promise_id: %s, polling for result...\n", promiseResp.PromiseID)
			return s.pollForCSV(ctx, promiseResp.PromiseID, client, req.Header, startDate, endDate)
		}
	}

	// Otherwise try to parse as CSV
	data, err := parseNYSEGCSVReader(strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, fmt.Errorf("parsing CSV response: %w", err)
	}

	return data, nil
}

// pollForCSV polls the API with the promise_id until the CSV is ready
func (s *NYSEGDirectScraper) pollForCSV(ctx context.Context, promiseID string, client *http.Client, headers http.Header, startDate, endDate time.Time) ([]models.UsageData, error) {
	// The actual polling endpoint is /promix/{promise_id}, not /usage/usage/download
	pollURL := fmt.Sprintf("https://engage-api-gw-dod79bsd.ue.gateway.dev/promix/%s", promiseID)

	maxAttempts := 30
	pollInterval := 2 * time.Second

	fmt.Printf("Polling with URL: %s\n", pollURL)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(pollInterval)
		}

		fmt.Printf("Polling attempt %d/%d...\n", attempt+1, maxAttempts)

		req, err := http.NewRequestWithContext(ctx, "GET", pollURL, nil)
		if err != nil {
			return nil, fmt.Errorf("creating poll request: %w", err)
		}

		// Copy headers from original request
		req.Header = headers.Clone()

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("polling request: %w", err)
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()

		if err != nil {
			return nil, fmt.Errorf("reading poll response: %w", err)
		}

		// Debug: show response on first and every 5th attempt
		if attempt == 0 || attempt == 5 {
			preview := string(bodyBytes)
			if len(preview) > 200 {
				preview = preview[:200]
			}
			fmt.Printf("   Response (HTTP %d): %s\n", resp.StatusCode, preview)
		}

		// Check if we got CSV
		contentType := resp.Header.Get("Content-Type")
		if strings.Contains(contentType, "csv") || strings.Contains(contentType, "text") {
			fmt.Println("✓ CSV ready, parsing...")
			return parseNYSEGCSVReader(strings.NewReader(string(bodyBytes)))
		}

		// Check response for promise status
		var promiseResp struct {
			Code       string `json:"code"`
			PromiseURL string `json:"promise_url"`
		}
		if err := json.Unmarshal(bodyBytes, &promiseResp); err == nil {
			if attempt == 0 || attempt == 5 {
				fmt.Printf("   Code: %s\n", promiseResp.Code)
			}

			// Check if data is ready - try fetching even if partial after a few attempts
			if promiseResp.PromiseURL != "" && (promiseResp.Code == "PROMISE_FOUND" || (attempt > 5 && promiseResp.Code == "PROMISE_FOUND_PARTIAL_DATA")) {
				fmt.Printf("✓ Data available (code: %s), fetching CSV from S3: %s\n", promiseResp.Code, promiseResp.PromiseURL)
				// Fetch the CSV from S3
				csvReq, err := http.NewRequestWithContext(ctx, "GET", promiseResp.PromiseURL, nil)
				if err != nil {
					return nil, fmt.Errorf("creating S3 request: %w", err)
				}

				csvResp, err := client.Do(csvReq)
				if err != nil {
					return nil, fmt.Errorf("fetching CSV from S3: %w", err)
				}
				defer csvResp.Body.Close()

				if csvResp.StatusCode != http.StatusOK {
					body, _ := io.ReadAll(csvResp.Body)
					return nil, fmt.Errorf("S3 returned status %d: %s", csvResp.StatusCode, string(body))
				}

				return parseNYSEGCSVReader(csvResp.Body)
			}

			// Check for failure
			if strings.Contains(promiseResp.Code, "ERROR") || strings.Contains(promiseResp.Code, "FAILED") {
				return nil, fmt.Errorf("CSV generation failed with code: %s", promiseResp.Code)
			}

			// Otherwise still waiting (PROMISE_FOUND_PARTIAL_DATA or similar)
		}
	}

	return nil, fmt.Errorf("CSV generation timed out after %d attempts", maxAttempts)
}

// extractAuthTokenFromBrowser uses chromedp to navigate to the page and extract the auth token
func (s *NYSEGDirectScraper) extractAuthTokenFromBrowser(ctx context.Context) (string, error) {
	fmt.Println("Extracting auth token from browser session...")

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-features", "IsolateOrigins,site-per-process"),
		chromedp.Flag("disable-http2", true),
		chromedp.Flag("disable-quic", true),
		chromedp.UserAgent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(ctx, opts...)
	defer cancel()

	browserCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	browserCtx, cancel = context.WithTimeout(browserCtx, 30*time.Second)
	defer cancel()

	// Set cookies
	if err := SetCookies(browserCtx, s.cookies); err != nil {
		return "", fmt.Errorf("setting cookies: %w", err)
	}

	// Navigate and extract token from localStorage or by intercepting requests
	var token string
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(nysegInsightsURL),
		chromedp.WaitVisible(`div.engage-insights-explore`, chromedp.ByQuery),
		chromedp.Evaluate(`
			(function() {
				// Try localStorage
				const lsToken = localStorage.getItem('up-authorization') ||
				                localStorage.getItem('auth_token') ||
				                localStorage.getItem('access_token');
				if (lsToken) return lsToken;

				// Try sessionStorage
				const ssToken = sessionStorage.getItem('up-authorization') ||
				                sessionStorage.getItem('auth_token') ||
				                sessionStorage.getItem('access_token');
				if (ssToken) return ssToken;

				// Try to find it in any global variable
				if (window.upAuthToken) return window.upAuthToken;
				if (window.authToken) return window.authToken;

				return null;
			})()
		`, &token),
	); err != nil {
		return "", fmt.Errorf("extracting token: %w", err)
	}

	if token == "" || token == "null" {
		return "", fmt.Errorf("could not find Up-Authorization token in browser storage")
	}

	fmt.Println("✓ Auth token extracted successfully")
	return token, nil
}

// parseNYSEGCSVReader parses NYSEG CSV from a reader
func parseNYSEGCSVReader(r io.Reader) ([]models.UsageData, error) {
	reader := csv.NewReader(r)

	// Read header to find column indices
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("reading CSV header: %w", err)
	}

	// Find column indices
	dateCol := -1
	startTimeCol := -1
	endTimeCol := -1
	usageCol := -1

	// Debug: print CSV headers
	fmt.Printf("CSV Headers: %v\n", header)

	for i, col := range header {
		colLower := strings.ToLower(strings.TrimSpace(col))
		switch {
		case strings.Contains(colLower, "date") && !strings.Contains(colLower, "time"):
			dateCol = i
		case strings.Contains(colLower, "start time"):
			startTimeCol = i
		case strings.Contains(colLower, "end time"):
			endTimeCol = i
		case strings.Contains(colLower, "usage"):
			usageCol = i
		}
	}

	fmt.Printf("Found columns - date: %d, startTime: %d, endTime: %d, usage: %d\n", dateCol, startTimeCol, endTimeCol, usageCol)

	if dateCol == -1 || usageCol == -1 {
		return nil, fmt.Errorf("could not find required columns (date and usage) in CSV. Header: %v", header)
	}

	// Parse data rows
	var results []models.UsageData

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading CSV row: %w", err)
		}

		if len(record) <= usageCol {
			continue
		}

		// Parse date
		dateStr := strings.TrimSpace(record[dateCol])
		if dateStr == "" {
			continue
		}

		date, err := parseNYSEGDate(dateStr)
		if err != nil {
			// Skip rows we can't parse
			continue
		}

		// Parse usage (kWh)
		usageStr := strings.TrimSpace(record[usageCol])
		usage, err := parseKWh(usageStr)
		if err != nil || usage == 0 {
			continue
		}

		// Parse end time if available
		var endTime time.Time
		if endTimeCol != -1 && len(record) > endTimeCol {
			endTimeStr := strings.TrimSpace(record[endTimeCol])
			if endTimeStr != "" {
				endTime, err = parseNYSEGDate(endTimeStr)
				if err != nil {
					// Debug: print first few failures
					if len(results) < 3 {
						fmt.Printf("Debug: Failed to parse end time '%s': %v\n", endTimeStr, err)
					}
					endTime = time.Time{}
				} else if len(results) < 3 {
					// Debug: print first few successes
					fmt.Printf("Debug: Successfully parsed end time '%s' -> %s\n", endTimeStr, endTime.Format("2006-01-02 15:04:05"))
				}
			}
		}

		// Parse start time if available
		var startTime time.Time
		if startTimeCol != -1 && len(record) > startTimeCol {
			startTimeStr := strings.TrimSpace(record[startTimeCol])
			if startTimeStr != "" {
				startTime, err = parseNYSEGDate(startTimeStr)
				if err != nil {
					// Debug: print first few failures
					if len(results) < 3 {
						fmt.Printf("Debug: Failed to parse start time '%s': %v\n", startTimeStr, err)
					}
					startTime = time.Time{}
				} else if len(results) < 3 {
					// Debug: print first few successes
					fmt.Printf("Debug: Successfully parsed start time '%s' -> %s\n", startTimeStr, startTime.Format("2006-01-02 15:04:05"))
				}
			}
		}

		// Store each hourly record separately (no aggregation)
		results = append(results, models.UsageData{
			Date:      date,
			StartTime: startTime,
			EndTime:   endTime,
			KWh:       usage,
		})
	}

	return results, nil
}
