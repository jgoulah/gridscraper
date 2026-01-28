package scraper

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/chromedp"
	"github.com/jgoulah/gridscraper/internal/config"
	"github.com/jgoulah/gridscraper/pkg/models"
)

const nysegInsightsURL = "https://energymanager.nyseg.com/insights"

// NYSEGScraper scrapes data from NYSEG
type NYSEGScraper struct {
	cookies []config.Cookie
	visible bool
}

// NewNYSEGScraper creates a new NYSEG scraper
func NewNYSEGScraper(cookies []config.Cookie, visible bool) *NYSEGScraper {
	return &NYSEGScraper{
		cookies: cookies,
		visible: visible,
	}
}

// Scrape fetches usage data from NYSEG by downloading CSV
func (s *NYSEGScraper) Scrape(ctx context.Context, daysToFetch int) ([]models.UsageData, error) {
	// Create temp directory for downloads
	downloadDir, err := os.MkdirTemp("", "gridscraper-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(downloadDir)

	// Create browser context with download directory
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", !s.visible),
		chromedp.Flag("no-sandbox", true),              // Required for running as root on Linux
		chromedp.Flag("disable-gpu", true),             // Recommended for headless Linux
		chromedp.Flag("disable-dev-shm-usage", true),   // Avoid /dev/shm issues on Linux
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.UserAgent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(ctx, opts...)
	defer cancel()

	browserCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	// Set timeout
	browserCtx, cancel = context.WithTimeout(browserCtx, 3*time.Minute)
	defer cancel()

	// Set download behavior
	if err := chromedp.Run(browserCtx,
		browser.SetDownloadBehavior(browser.SetDownloadBehaviorBehaviorAllowAndName).
			WithDownloadPath(downloadDir).
			WithEventsEnabled(true),
	); err != nil {
		return nil, fmt.Errorf("setting download behavior: %w", err)
	}

	// Set cookies and navigate
	if err := SetCookies(browserCtx, s.cookies); err != nil {
		return nil, fmt.Errorf("setting cookies: %w", err)
	}

	// Navigate to insights page
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(nysegInsightsURL),
		chromedp.WaitVisible(`div.engage-insights-explore`, chromedp.ByQuery),
	); err != nil {
		return nil, fmt.Errorf("navigating to insights page: %w", err)
	}

	// Click the download button
	fmt.Println("Clicking download link...")
	if err := chromedp.Run(browserCtx,
		chromedp.Click(`div#engage-insights-explore__download-button`, chromedp.ByQuery),
		chromedp.Sleep(2*time.Second),
	); err != nil {
		return nil, fmt.Errorf("clicking download link: %w", err)
	}

	// Wait for popup/modal and select options
	if err := chromedp.Run(browserCtx,
		chromedp.WaitVisible(`input#bill-period`, chromedp.ByQuery),
		chromedp.Click(`input#bill-period`, chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
		chromedp.Click(`input#csv`, chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
	); err != nil {
		return nil, fmt.Errorf("selecting download options: %w", err)
	}

	// Find and click the download button using JavaScript
	fmt.Println("Looking for download button...")
	var buttonFound bool
	if err := chromedp.Run(browserCtx,
		chromedp.Evaluate(`
			(function() {
				// Look for buttons with download/export text
				const buttons = document.querySelectorAll('button, input[type="submit"], a.button');
				for (let btn of buttons) {
					const text = btn.textContent.toLowerCase();
					if (text.includes('download') || text.includes('export') || btn.type === 'submit') {
						btn.click();
						return true;
					}
				}
				return false;
			})()
		`, &buttonFound),
	); err != nil {
		return nil, fmt.Errorf("searching for download button: %w", err)
	}

	if !buttonFound {
		return nil, fmt.Errorf("could not find download button")
	}

	fmt.Println("Download button clicked, waiting for file...")

	// Wait for download to complete
	time.Sleep(5 * time.Second)

	// Find the downloaded CSV file
	files, err := os.ReadDir(downloadDir)
	if err != nil {
		return nil, fmt.Errorf("reading download directory: %w", err)
	}

	var csvPath string
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".csv") {
			csvPath = filepath.Join(downloadDir, file.Name())
			break
		}
	}

	if csvPath == "" {
		return nil, fmt.Errorf("no CSV file downloaded")
	}

	// Parse the CSV
	data, err := parseNYSEGCSV(csvPath)
	if err != nil {
		return nil, fmt.Errorf("parsing CSV: %w", err)
	}

	return data, nil
}

// parseNYSEGCSV parses the downloaded NYSEG CSV file
func parseNYSEGCSV(path string) ([]models.UsageData, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening CSV: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)

	// Read header to find column indices
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("reading CSV header: %w", err)
	}

	// Find column indices
	dateCol := -1
	usageCol := -1
	// Future: could also extract startTimeCol and weatherCol if needed

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
		return nil, fmt.Errorf("could not find required columns (date and usage) in CSV")
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

// parseNYSEGDate attempts to parse date from NYSEG CSV format
func parseNYSEGDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)

	// Try various date/time formats that NYSEG might use
	formats := []string{
		"2006-01-02 15:04:05-07:00",  // ISO 8601 with timezone (End Time column)
		"2006-01-02T15:04:05-07:00",  // ISO 8601 variant
		"2006-01-02 15:04:05",        // Datetime without timezone
		"1/2/2006",
		"01/02/2006",
		"2006-01-02",
		"1/2/06",
		"01/02/06",
		"Jan 2, 2006",
		"January 2, 2006",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, s); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unable to parse date: %s", s)
}

// parseKWh attempts to parse a kWh value from a string
func parseKWh(s string) (float64, error) {
	// Remove common formatting characters
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", "")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ToLower(s)
	s = strings.TrimSuffix(s, "kwh")
	s = strings.TrimSpace(s)

	if s == "" {
		return 0, fmt.Errorf("empty string")
	}

	return strconv.ParseFloat(s, 64)
}
