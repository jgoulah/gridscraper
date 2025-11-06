package scraper

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/jgoulah/gridscraper/internal/config"
	"github.com/jgoulah/gridscraper/pkg/models"
)

const nysegAPIURL = "https://engage-api-gw-dod79bsd.ue.gateway.dev/usage/usage/download"

// NYSEGDirectScraper scrapes data from NYSEG using direct API calls
type NYSEGDirectScraper struct {
	cookies []config.Cookie
	authToken string
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

// Scrape fetches usage data from NYSEG API
func (s *NYSEGDirectScraper) Scrape(ctx context.Context) ([]models.UsageData, error) {
	// If we don't have an auth token, we need to get it from the browser
	if s.authToken == "" {
		token, err := s.extractAuthTokenFromBrowser(ctx)
		if err != nil {
			return nil, fmt.Errorf("extracting auth token: %w", err)
		}
		s.authToken = token
	}
	// Calculate date range (last 30 days by default)
	endDate := time.Now()
	startDate := endDate.AddDate(0, -1, 0) // 1 month ago

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

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Check content type
	contentType := resp.Header.Get("Content-Type")
	fmt.Printf("Response content-type: %s\n", contentType)

	// Parse CSV response
	data, err := parseNYSEGCSVReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parsing CSV response: %w", err)
	}

	return data, nil
}

// extractAuthTokenFromBrowser uses chromedp to navigate to the page and extract the auth token
func (s *NYSEGDirectScraper) extractAuthTokenFromBrowser(ctx context.Context) (string, error) {
	fmt.Println("Extracting auth token from browser session...")

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
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
	usageCol := -1

	for i, col := range header {
		colLower := strings.ToLower(strings.TrimSpace(col))
		switch {
		case strings.Contains(colLower, "date"):
			dateCol = i
		case strings.Contains(colLower, "usage"):
			usageCol = i
		}
	}

	if dateCol == -1 || usageCol == -1 {
		return nil, fmt.Errorf("could not find required columns (date and usage) in CSV. Header: %v", header)
	}

	// Parse data rows
	var results []models.UsageData
	seenDates := make(map[string]bool)

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

		// Aggregate by date (sum all readings for the same day)
		dateKey := date.Format("2006-01-02")
		if seenDates[dateKey] {
			// Find existing record and add to it
			for i := range results {
				if results[i].Date.Format("2006-01-02") == dateKey {
					results[i].KWh += usage
					break
				}
			}
		} else {
			seenDates[dateKey] = true
			results = append(results, models.UsageData{
				Date: date,
				KWh:  usage,
			})
		}
	}

	return results, nil
}
