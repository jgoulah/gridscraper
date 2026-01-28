package scraper

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/jgoulah/gridscraper/internal/config"
	"github.com/jgoulah/gridscraper/pkg/models"
)

const nysegAPIURL = "https://engage-api-gw-dod79bsd.ue.gateway.dev/v2/usage"

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
	visible   bool
}

// SetVisible sets whether to show the browser window
func (s *NYSEGDirectScraper) SetVisible(visible bool) {
	s.visible = visible
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
		chromedp.Flag("no-sandbox", true),            // Required for running as root on Linux
		chromedp.Flag("disable-gpu", true),           // Recommended for headless Linux
		chromedp.Flag("disable-dev-shm-usage", true), // Avoid /dev/shm issues on Linux
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
			// Debug: show API requests to engage-api
			if strings.Contains(ev.Request.URL, "engage-api") {
				fmt.Printf("  [DEBUG] API request: %s\n", ev.Request.URL)
				for k, v := range ev.Request.Headers {
					if strings.Contains(strings.ToLower(k), "auth") {
						fmt.Printf("  [DEBUG]   Header %s: %v\n", k, v)
					}
				}
			}
			// Capture auth token from any request (check both header names)
			if !tokenCaptured {
				// Try Up-Authorization first
				if authToken, ok := ev.Request.Headers["Up-Authorization"]; ok {
					if authStr, ok := authToken.(string); ok && authStr != "" {
						capturedAuthToken = authStr
						tokenCaptured = true
						fmt.Printf("  [DEBUG] Captured Up-Authorization token\n")
					}
				}
				// Also try X-Authentication as fallback
				if !tokenCaptured {
					if authToken, ok := ev.Request.Headers["X-Authentication"]; ok {
						if authStr, ok := authToken.(string); ok && authStr != "" {
							capturedAuthToken = authStr
							tokenCaptured = true
							fmt.Printf("  [DEBUG] Captured X-Authentication token\n")
						}
					}
				}
			}
		}
	})

	// Perform login
	const loginURL = "https://sso.nyseg.com/es/login"
	fmt.Printf("  [DEBUG] Navigating to login: %s\n", loginURL)
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

	// Check current URL after login
	var currentURL string
	if err := chromedp.Run(browserCtx, chromedp.Location(&currentURL)); err == nil {
		fmt.Printf("  [DEBUG] URL after login: %s\n", currentURL)
	}

	// Navigate to insights to trigger API calls that will include the auth token
	fmt.Printf("  [DEBUG] Navigating to insights: %s\n", nysegInsightsURL)
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(nysegInsightsURL),
		chromedp.Sleep(3*time.Second), // Wait for page to load
		chromedp.WaitVisible(`div.engage-insights-explore`, chromedp.ByQuery),
		chromedp.Sleep(3*time.Second), // Wait for API calls to complete
	); err != nil {
		return nil, "", fmt.Errorf("navigating to insights: %w", err)
	}

	// Check final URL
	if err := chromedp.Run(browserCtx, chromedp.Location(&currentURL)); err == nil {
		fmt.Printf("  [DEBUG] Final URL: %s\n", currentURL)
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

// Scrape fetches usage data from NYSEG using browser-based CSV download
// The v2/usage API only returns billing period summaries, so we use browser
// automation to download the CSV which contains hourly data.
func (s *NYSEGDirectScraper) Scrape(ctx context.Context, daysToFetch int) ([]models.UsageData, error) {
	if s.username == "" || s.password == "" {
		return nil, fmt.Errorf("username and password required for NYSEG scraping")
	}

	fmt.Println("Starting browser-based CSV download for hourly data...")

	// Create temp directory for downloads
	downloadDir, err := os.MkdirTemp("", "gridscraper-nyseg-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	fmt.Printf("Download directory: %s\n", downloadDir)
	defer os.RemoveAll(downloadDir)

	// Build browser options - use new headless mode which is less detectable
	opts := []chromedp.ExecAllocatorOption{
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-features", "IsolateOrigins,site-per-process"),
		// Window size helps with proper rendering
		chromedp.WindowSize(1920, 1080),
		chromedp.UserAgent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"),
	}

	// Use new headless mode (--headless=new) which renders like regular Chrome
	// Old headless mode is easily detected and renders differently
	if s.visible {
		opts = append(opts,
			chromedp.Flag("remote-debugging-port", "9222"),
			chromedp.UserDataDir("/tmp/chrome-debug-profile"),
		)
	} else {
		// New headless mode - much better compatibility with modern sites
		opts = append(opts, chromedp.Flag("headless", "new"))
	}

	allocCtx, cancel := chromedp.NewExecAllocator(ctx, opts...)
	defer cancel()

	browserCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	browserCtx, cancel = context.WithTimeout(browserCtx, 3*time.Minute)
	defer cancel()

	// Track auth token and x-authentication for direct API calls
	var capturedUpAuth string
	var capturedXAuth string
	var tokenMu sync.Mutex

	// Listen for network requests to capture auth tokens
	chromedp.ListenTarget(browserCtx, func(ev interface{}) {
		switch ev := ev.(type) {
		case *network.EventRequestWillBeSent:
			// Capture auth tokens from API requests
			if strings.Contains(ev.Request.URL, "engage-api") {
				tokenMu.Lock()
				if capturedUpAuth == "" {
					if authToken, ok := ev.Request.Headers["Up-Authorization"]; ok {
						if authStr, ok := authToken.(string); ok && authStr != "" {
							capturedUpAuth = authStr
							fmt.Printf("  [AUTH] Captured Up-Authorization token\n")
						}
					}
				}
				if capturedXAuth == "" {
					if xAuth, ok := ev.Request.Headers["X-Authentication"]; ok {
						if xAuthStr, ok := xAuth.(string); ok && xAuthStr != "" {
							capturedXAuth = xAuthStr
							fmt.Printf("  [AUTH] Captured X-Authentication token\n")
						}
					}
				}
				tokenMu.Unlock()
			}
		}
	})

	// Enable network monitoring
	if err := chromedp.Run(browserCtx,
		network.Enable(),
	); err != nil {
		return nil, fmt.Errorf("enabling network: %w", err)
	}

	// Perform login
	const loginURL = "https://sso.nyseg.com/es/login"
	fmt.Println("Logging in to NYSEG...")
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(loginURL),
		chromedp.Sleep(2*time.Second),
		chromedp.WaitVisible(`input#_com_liferay_login_web_portlet_LoginPortlet_login`, chromedp.ByQuery),
		chromedp.SendKeys(`input#_com_liferay_login_web_portlet_LoginPortlet_login`, s.username, chromedp.ByQuery),
		chromedp.SendKeys(`input#_com_liferay_login_web_portlet_LoginPortlet_password`, s.password, chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
		chromedp.Click(`button[type="submit"]`, chromedp.ByQuery),
	); err != nil {
		return nil, fmt.Errorf("login failed: %w", err)
	}

	// Wait a bit for login to process, then explicitly navigate to insights
	// (the NYSEG login page doesn't auto-redirect, we need to navigate manually)
	fmt.Println("Waiting for login to process...")
	if err := chromedp.Run(browserCtx,
		chromedp.Sleep(5*time.Second),
	); err != nil {
		return nil, fmt.Errorf("waiting after login: %w", err)
	}

	// Debug: show where we are
	var currentURL string
	if err := chromedp.Run(browserCtx, chromedp.Location(&currentURL)); err == nil {
		fmt.Printf("After login form submit, URL: %s\n", currentURL)
	}

	// Navigate to insights page (don't wait for auto-redirect, navigate explicitly)
	fmt.Println("Navigating to insights page...")
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(nysegInsightsURL),
		chromedp.WaitVisible(`div.engage-insights-explore`, chromedp.ByQuery),
		chromedp.Sleep(2*time.Second),
	); err != nil {
		return nil, fmt.Errorf("navigating to insights page: %w", err)
	}

	// Wait for page to load and API calls to complete (which captures auth tokens)
	fmt.Println("Waiting for page to fully load and capture auth tokens...")
	if err := chromedp.Run(browserCtx, chromedp.Sleep(5*time.Second)); err != nil {
		return nil, fmt.Errorf("waiting for page: %w", err)
	}

	// In visible mode, pause longer so DevTools can attach and user can interact
	if s.visible {
		fmt.Println("=== VISIBLE MODE: Pausing for 60 seconds for DevTools attachment ===")
		fmt.Println("=== Connect to chrome://inspect or use remote-debugging-port 9222 ===")
		if err := chromedp.Run(browserCtx, chromedp.Sleep(60*time.Second)); err != nil {
			return nil, fmt.Errorf("debug pause: %w", err)
		}
	}

	// Check if we captured the auth tokens
	tokenMu.Lock()
	upAuth := capturedUpAuth
	tokenMu.Unlock()

	if upAuth == "" {
		return nil, fmt.Errorf("could not capture Up-Authorization token from API requests")
	}

	// Calculate date range (last N days)
	endDate := time.Now()
	startDate := endDate.AddDate(0, 0, -daysToFetch)

	// Set up listener to capture the download API response (promise_id)
	var capturedPromiseID string
	var downloadRequestID network.RequestID
	var downloadMu sync.Mutex
	promiseIDChan := make(chan string, 1) // Channel to signal when promise_id is captured

	// Add a new listener for the download response
	chromedp.ListenTarget(browserCtx, func(ev interface{}) {
		switch ev := ev.(type) {
		case *network.EventRequestWillBeSent:
			// Log headers for download requests
			if strings.Contains(ev.Request.URL, "usage/usage/download") {
				fmt.Printf("  [NETWORK] Download API request: %s %s\n", ev.Request.Method, ev.Request.URL)
				downloadMu.Lock()
				downloadRequestID = ev.RequestID
				downloadMu.Unlock()
			}
		case *network.EventResponseReceived:
			// Look for the download API response
			if strings.Contains(ev.Response.URL, "usage/usage/download") {
				fmt.Printf("  [NETWORK] Download API response: status=%d\n", ev.Response.Status)
			}
		case *network.EventLoadingFinished:
			// Check if this is the download request finishing
			downloadMu.Lock()
			reqID := downloadRequestID
			downloadMu.Unlock()
			if ev.RequestID == reqID && reqID != "" {
				// Get the response body in a goroutine
				go func() {
					var body []byte
					if err := chromedp.Run(browserCtx, chromedp.ActionFunc(func(ctx context.Context) error {
						var err error
						body, err = network.GetResponseBody(reqID).Do(ctx)
						return err
					})); err != nil {
						fmt.Printf("  [NETWORK] Could not get response body: %v\n", err)
						return
					}
					// Parse the promise_id from the response
					var resp struct {
						PromiseID string `json:"promise_id"`
					}
					if err := json.Unmarshal(body, &resp); err == nil && resp.PromiseID != "" {
						downloadMu.Lock()
						capturedPromiseID = resp.PromiseID
						downloadMu.Unlock()
						fmt.Printf("  [NETWORK] Captured promise_id: %s\n", resp.PromiseID)
						// Signal that we got the promise_id
						select {
						case promiseIDChan <- resp.PromiseID:
						default:
						}
					} else {
						fmt.Printf("  [NETWORK] Response body: %s\n", string(body))
					}
				}()
			}
		}
	})

	// Click on "Download my energy usage data" to open the modal
	fmt.Println("Opening download modal...")
	var modalResult string
	if err := chromedp.Run(browserCtx,
		chromedp.Evaluate(`
			(function() {
				// Find and click the download link/button
				const elements = document.querySelectorAll('*');
				for (const el of elements) {
					if (el.textContent && el.textContent.trim() === 'Download my energy usage data') {
						console.log('Found download element:', el.tagName, el.className);
						el.click();
						return 'CLICKED: ' + el.tagName + '.' + el.className;
					}
				}
				return 'NOT_FOUND';
			})()
		`, &modalResult),
		chromedp.Sleep(3*time.Second), // Wait for modal to open
	); err != nil {
		return nil, fmt.Errorf("opening download modal: %w", err)
	}
	fmt.Printf("Modal open result: %s\n", modalResult)
	if modalResult == "NOT_FOUND" {
		return nil, fmt.Errorf("could not find 'Download my energy usage data' button")
	}

	// Wait for modal to actually appear by checking for "Select File Format" text
	var modalVisible bool
	for i := 0; i < 5; i++ {
		if err := chromedp.Run(browserCtx,
			chromedp.Evaluate(`document.body.innerText.includes('Select File Format')`, &modalVisible),
		); err != nil {
			return nil, fmt.Errorf("checking for modal: %w", err)
		}
		if modalVisible {
			fmt.Println("Modal is visible")
			break
		}
		fmt.Printf("Waiting for modal to appear (attempt %d/5)...\n", i+1)
		if err := chromedp.Run(browserCtx, chromedp.Sleep(1*time.Second)); err != nil {
			return nil, fmt.Errorf("waiting for modal: %w", err)
		}
	}
	if !modalVisible {
		return nil, fmt.Errorf("modal did not appear after clicking download button")
	}

	// Select CSV format and click download
	fmt.Println("Selecting CSV format and initiating download...")

	var csvResult string
	if err := chromedp.Run(browserCtx,
		// Click on CSV radio button with proper events
		chromedp.Evaluate(`
			(function() {
				// Helper to click with proper events
				function clickWithEvents(el) {
					el.focus();
					el.dispatchEvent(new MouseEvent('mousedown', {bubbles: true, cancelable: true, view: window}));
					el.dispatchEvent(new MouseEvent('mouseup', {bubbles: true, cancelable: true, view: window}));
					el.dispatchEvent(new MouseEvent('click', {bubbles: true, cancelable: true, view: window}));
					// Also dispatch change event for form elements
					el.dispatchEvent(new Event('change', {bubbles: true}));
				}

				// Approach 1: Find input[type=radio] with label containing "CSV"
				const inputRadios = document.querySelectorAll('input[type="radio"]');
				for (const radio of inputRadios) {
					// Check the associated label
					if (radio.labels && radio.labels.length > 0) {
						const labelText = radio.labels[0].innerText.trim();
						if (labelText === 'CSV') {
							clickWithEvents(radio);
							// Also click the label for good measure
							clickWithEvents(radio.labels[0]);
							return 'clicked input[type=radio] with label "CSV" (checked=' + radio.checked + ')';
						}
					}
					// Also check parent element text
					const parent = radio.parentElement;
					if (parent) {
						const parentText = parent.innerText.trim();
						if (parentText === 'CSV') {
							clickWithEvents(radio);
							return 'clicked input[type=radio] with parent text "CSV" (checked=' + radio.checked + ')';
						}
					}
				}

				// Approach 2: Find role="radio" elements containing "CSV"
				const roleRadios = document.querySelectorAll('[role="radio"]');
				for (const radio of roleRadios) {
					const text = (radio.innerText || radio.textContent || '').trim();
					if (text.includes('CSV') && !text.includes('XML')) {
						clickWithEvents(radio);
						return 'clicked [role=radio] containing CSV: ' + text;
					}
				}

				// Approach 3: Find label element containing "CSV" and click it
				const labels = document.querySelectorAll('label');
				for (const label of labels) {
					if (label.innerText.trim() === 'CSV') {
						clickWithEvents(label);
						return 'clicked label with text "CSV"';
					}
				}

				return 'CSV not found - no matching elements';
			})()
		`, &csvResult),
		chromedp.Sleep(1*time.Second),
	); err != nil {
		return nil, fmt.Errorf("selecting CSV format: %w", err)
	}
	fmt.Printf("CSV selection result: %s\n", csvResult)

	// Click the Download file button
	fmt.Println("Clicking Download file button...")

	// Intercept fetch/XMLHttpRequest to capture the download response
	if err := chromedp.Run(browserCtx,
		chromedp.Evaluate(`
			// Intercept the download API response
			window.__downloadPromiseID = null;
			window.__downloadError = null;

			// Hook into fetch
			const originalFetch = window.fetch;
			window.fetch = async function(...args) {
				const response = await originalFetch.apply(this, args);
				const url = args[0];
				if (url && url.toString().includes('usage/usage/download')) {
					const clonedResponse = response.clone();
					try {
						const json = await clonedResponse.json();
						if (json.promise_id) {
							window.__downloadPromiseID = json.promise_id;
							console.log('Captured promise_id:', json.promise_id);
						}
					} catch (e) {
						console.log('Could not parse download response:', e);
					}
				}
				return response;
			};
			true
		`, nil),
	); err != nil {
		return nil, fmt.Errorf("setting up fetch interceptor: %w", err)
	}

	// Now click the download button using native chromedp click (sends real mouse events)

	// First, find the button and get its selector
	var buttonSelector string
	if err := chromedp.Run(browserCtx,
		chromedp.Evaluate(`
			(function() {
				// Find "Download file" button and return a unique selector
				const buttons = document.querySelectorAll('button, span, div');
				for (const el of buttons) {
					const text = el.textContent.trim().toLowerCase();
					if (text === 'download file') {
						// Try to build a unique selector
						if (el.id) return '#' + el.id;
						if (el.className) {
							const classes = el.className.split(' ').filter(c => c).join('.');
							return el.tagName.toLowerCase() + '.' + classes;
						}
						return el.tagName.toLowerCase() + ':contains("Download file")';
					}
				}
				return '';
			})()
		`, &buttonSelector),
	); err != nil {
		return nil, fmt.Errorf("finding download button: %w", err)
	}

	// Use chromedp's native click with the selector, or fall back to JavaScript
	if buttonSelector != "" && !strings.Contains(buttonSelector, ":contains") {
		if err := chromedp.Run(browserCtx,
			chromedp.Click(buttonSelector, chromedp.ByQuery),
			chromedp.Sleep(2*time.Second),
		); err != nil {
			fmt.Printf("Native click failed: %v, trying JavaScript click\n", err)
			// Fall back to JavaScript click
			if err := chromedp.Run(browserCtx,
				chromedp.Evaluate(`
					(function() {
						const buttons = document.querySelectorAll('button, span, div');
						for (const el of buttons) {
							if (el.textContent.trim().toLowerCase() === 'download file') {
								// Dispatch proper mouse events
								el.dispatchEvent(new MouseEvent('mousedown', {bubbles: true, cancelable: true, view: window}));
								el.dispatchEvent(new MouseEvent('mouseup', {bubbles: true, cancelable: true, view: window}));
								el.dispatchEvent(new MouseEvent('click', {bubbles: true, cancelable: true, view: window}));
								return 'JS click with events: ' + el.tagName;
							}
						}
						return 'NOT_FOUND';
					})()
				`, nil),
				chromedp.Sleep(2*time.Second),
			); err != nil {
				return nil, fmt.Errorf("clicking download button: %w", err)
			}
		}
		fmt.Println("Download button clicked via native chromedp")
	} else {
		// JavaScript click with proper events
		var downloadBtnResult string
		if err := chromedp.Run(browserCtx,
			chromedp.Evaluate(`
				(function() {
					const buttons = document.querySelectorAll('button, span, div');
					for (const el of buttons) {
						if (el.textContent.trim().toLowerCase() === 'download file') {
							// Dispatch proper mouse events to simulate real click
							el.dispatchEvent(new MouseEvent('mousedown', {bubbles: true, cancelable: true, view: window}));
							el.dispatchEvent(new MouseEvent('mouseup', {bubbles: true, cancelable: true, view: window}));
							el.dispatchEvent(new MouseEvent('click', {bubbles: true, cancelable: true, view: window}));
							return 'CLICKED with events: ' + el.tagName + '.' + el.className;
						}
					}
					return 'NOT_FOUND';
				})()
			`, &downloadBtnResult),
			chromedp.Sleep(2*time.Second),
		); err != nil {
			return nil, fmt.Errorf("clicking download button: %w", err)
		}
		fmt.Printf("Download button result: %s\n", downloadBtnResult)
		if downloadBtnResult == "NOT_FOUND" {
			return nil, fmt.Errorf("could not find 'Download file' button in modal")
		}
	}

	// Wait for promise_id from network listener (with timeout)
	fmt.Println("Waiting for download API response...")
	var promiseID string

	// Wait up to 15 seconds for the promise_id via channel
	select {
	case promiseID = <-promiseIDChan:
		fmt.Printf("Got promise_id from network listener: %s\n", promiseID)
	case <-time.After(15 * time.Second):
		fmt.Println("Timeout waiting for promise_id from network listener, trying fallbacks...")
	}

	// Fallback 1: Check the mutex-protected variable (in case goroutine completed but channel missed)
	if promiseID == "" {
		downloadMu.Lock()
		promiseID = capturedPromiseID
		downloadMu.Unlock()
		if promiseID != "" {
			fmt.Printf("Got promise_id from captured variable: %s\n", promiseID)
		}
	}

	// Fallback 2: Try fetch interceptor
	if promiseID == "" {
		var fetchPromiseID string
		if err := chromedp.Run(browserCtx,
			chromedp.Evaluate(`window.__downloadPromiseID`, &fetchPromiseID),
		); err == nil && fetchPromiseID != "" && fetchPromiseID != "null" {
			promiseID = fetchPromiseID
			fmt.Printf("Got promise_id from fetch interceptor: %s\n", promiseID)
		}
	}

	if promiseID == "" || promiseID == "null" {
		// Print diagnostic info
		fmt.Println("ERROR: Could not capture promise_id. Diagnostic info:")
		fmt.Printf("  - CSV selection result was: %s\n", csvResult)
		fmt.Println("  - Check if download API request appeared in [NETWORK] logs above")
		return nil, fmt.Errorf("could not capture promise_id from download request (check if CSV was selected)")
	}

	fmt.Printf("Got promise_id: %s, polling for CSV...\n", promiseID)

	// Create HTTP client for polling
	client := &http.Client{Timeout: 60 * time.Second}

	// Build headers for polling using captured auth token
	headers := http.Header{}
	headers.Set("Accept", "*/*")
	headers.Set("Accept-Language", "en-US,en;q=0.9")
	headers.Set("Content-Type", "application/json")
	headers.Set("Origin", "https://energymanager.nyseg.com")
	headers.Set("Referer", "https://energymanager.nyseg.com/")
	headers.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	headers.Set("Up-Authorization", upAuth)

	// Poll for CSV
	data, err := s.pollForCSV(ctx, promiseID, client, headers, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("polling for CSV: %w", err)
	}

	fmt.Printf("✓ Parsed %d hourly usage records\n", len(data))

	// Extract and save fresh cookies for future use
	freshCookies, err := ExtractCookies(browserCtx)
	if err == nil && len(freshCookies) > 0 {
		s.cookies = freshCookies
	}

	return data, nil
}

// parseV2UsageJSON parses the JSON response from the v2/usage endpoint
func parseV2UsageJSON(bodyBytes []byte) ([]models.UsageData, error) {
	// Debug: show first 500 chars of response
	preview := string(bodyBytes)
	if len(preview) > 500 {
		preview = preview[:500]
	}
	fmt.Printf("Response preview: %s\n", preview)

	// The v2/usage endpoint returns data with fields: start_time, end_time, total_amount
	type usageEntry struct {
		StartTime   string  `json:"start_time"`
		EndTime     string  `json:"end_time"`
		TotalAmount float64 `json:"total_amount"`
		PeakAmount  float64 `json:"peak_amount"`
		OffPeak     float64 `json:"off_peak_amount"`
	}

	var usageEntries []usageEntry

	if err := json.Unmarshal(bodyBytes, &usageEntries); err != nil {
		// Try parsing as an object with a data field
		var wrapper struct {
			Commodity string       `json:"commodity"`
			Data      []usageEntry `json:"data"`
		}
		if err2 := json.Unmarshal(bodyBytes, &wrapper); err2 != nil {
			return nil, fmt.Errorf("parsing JSON response: %w (body: %s)", err, string(bodyBytes)[:min(200, len(bodyBytes))])
		}
		usageEntries = wrapper.Data
	}

	fmt.Printf("Found %d entries in JSON response\n", len(usageEntries))

	var result []models.UsageData
	for _, entry := range usageEntries {
		// Parse start time
		var startTime time.Time
		var err error
		if entry.StartTime == "" {
			continue
		}

		// Try multiple date formats
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
			startTime, err = time.Parse(layout, entry.StartTime)
			if err == nil {
				break
			}
		}
		if err != nil {
			fmt.Printf("Warning: could not parse start_time %q, skipping\n", entry.StartTime)
			continue
		}

		// Use total_amount for usage
		usage := entry.TotalAmount

		// Parse end time if available
		var endTime time.Time
		if entry.EndTime != "" {
			for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
				endTime, err = time.Parse(layout, entry.EndTime)
				if err == nil {
					break
				}
			}
		}
		if endTime.IsZero() {
			endTime = startTime.Add(24 * time.Hour) // Default to 1 day interval for daily data
		}

		result = append(result, models.UsageData{
			Date:      startTime.Truncate(24 * time.Hour),
			StartTime: startTime,
			EndTime:   endTime,
			KWh:       usage,
			Service:   "nyseg",
		})
	}

	fmt.Printf("✓ Parsed %d usage entries\n", len(result))
	return result, nil
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

// pollForCSVViaBrowser polls the API using browser fetch() until CSV is ready
func (s *NYSEGDirectScraper) pollForCSVViaBrowser(browserCtx context.Context, promiseID string, startDate, endDate time.Time) ([]models.UsageData, error) {
	pollURL := fmt.Sprintf("https://engage-api-gw-dod79bsd.ue.gateway.dev/promix/%s", promiseID)

	maxAttempts := 30
	pollInterval := 2 * time.Second

	fmt.Printf("Polling with URL: %s\n", pollURL)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(pollInterval)
		}

		fmt.Printf("Polling attempt %d/%d...\n", attempt+1, maxAttempts)

		// Use browser fetch for polling
		setupJS := fmt.Sprintf(`
			window.__pollResult = null;
			window.__pollDone = false;
			fetch('%s', {
				method: 'GET',
				credentials: 'include'
			}).then(async response => {
				const text = await response.text();
				window.__pollResult = {
					status: response.status,
					contentType: response.headers.get('content-type'),
					body: text
				};
				window.__pollDone = true;
			}).catch(e => {
				window.__pollResult = {
					error: e.toString()
				};
				window.__pollDone = true;
			});
			true
		`, pollURL)

		if err := chromedp.Run(browserCtx, chromedp.Evaluate(setupJS, nil)); err != nil {
			return nil, fmt.Errorf("starting browser fetch: %w", err)
		}

		var fetchResult map[string]interface{}
		if err := chromedp.Run(browserCtx,
			chromedp.PollFunction(`() => window.__pollDone === true`, nil, chromedp.WithPollingInterval(500*time.Millisecond), chromedp.WithPollingTimeout(30*time.Second)),
			chromedp.Evaluate(`window.__pollResult`, &fetchResult),
		); err != nil {
			return nil, fmt.Errorf("browser fetch failed: %w", err)
		}

		if fetchResult == nil {
			return nil, fmt.Errorf("browser fetch returned nil result")
		}

		if errVal, ok := fetchResult["error"]; ok && errVal != nil {
			return nil, fmt.Errorf("browser fetch error: %v", errVal)
		}

		statusFloat, _ := fetchResult["status"].(float64)
		status := int(statusFloat)
		contentType, _ := fetchResult["contentType"].(string)
		body, _ := fetchResult["body"].(string)

		// Debug: show response on first and every 5th attempt
		if attempt == 0 || attempt == 5 {
			preview := body
			if len(preview) > 200 {
				preview = preview[:200]
			}
			fmt.Printf("   Response (HTTP %d): %s\n", status, preview)
		}

		// Check if we got CSV
		if strings.Contains(contentType, "csv") || strings.Contains(contentType, "text") {
			fmt.Println("✓ CSV ready, parsing...")
			return parseNYSEGCSVReader(strings.NewReader(body))
		}

		// Check response for promise status
		var promiseResp struct {
			Code       string `json:"code"`
			PromiseURL string `json:"promise_url"`
		}
		if err := json.Unmarshal([]byte(body), &promiseResp); err == nil {
			if attempt == 0 || attempt == 5 {
				fmt.Printf("   Code: %s\n", promiseResp.Code)
			}

			// Check if data is ready
			if promiseResp.PromiseURL != "" && (promiseResp.Code == "PROMISE_FOUND" || (attempt > 5 && promiseResp.Code == "PROMISE_FOUND_PARTIAL_DATA")) {
				fmt.Printf("✓ Data available (code: %s), fetching CSV from S3: %s\n", promiseResp.Code, promiseResp.PromiseURL)
				// Fetch CSV from S3 URL (this doesn't require auth headers)
				client := &http.Client{Timeout: 60 * time.Second}
				csvResp, err := client.Get(promiseResp.PromiseURL)
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
		}
	}

	return nil, fmt.Errorf("CSV generation timed out after %d attempts", maxAttempts)
}

// extractAuthTokenFromBrowser uses chromedp to navigate to the page and extract the auth token
func (s *NYSEGDirectScraper) extractAuthTokenFromBrowser(ctx context.Context) (string, error) {
	fmt.Println("Extracting auth token from browser session...")

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("no-sandbox", true),            // Required for running as root on Linux
		chromedp.Flag("disable-gpu", true),           // Recommended for headless Linux
		chromedp.Flag("disable-dev-shm-usage", true), // Avoid /dev/shm issues on Linux
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
		return "", fmt.Errorf("could not find X-Authentication token in browser storage")
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
					endTime = time.Time{}
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
					startTime = time.Time{}
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
