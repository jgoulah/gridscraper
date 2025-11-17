package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/jgoulah/gridscraper/internal/config"
	"github.com/jgoulah/gridscraper/internal/scraper"
	"github.com/spf13/cobra"
)

var captureCmd = &cobra.Command{
	Use:   "capture [service]",
	Short: "Capture network request for CSV download",
	Long:  `Opens browser, waits for you to click download, and captures the request details.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runCapture,
}

func init() {
	rootCmd.AddCommand(captureCmd)
}

func runCapture(cmd *cobra.Command, args []string) error {
	service := args[0]

	var loginURL string
	switch service {
	case "nyseg":
		loginURL = "https://energymanager.nyseg.com/insights"
	case "coned":
		return fmt.Errorf("Con Edison support not yet implemented")
	default:
		return fmt.Errorf("unknown service: %s (available: nyseg, coned)", service)
	}

	// Load config
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Get cookies
	var cookies []config.Cookie
	switch service {
	case "nyseg":
		cookies = cfg.Cookies.NYSEG
	case "coned":
		cookies = cfg.Cookies.ConEd
	}

	if len(cookies) == 0 {
		return fmt.Errorf("no cookies found for %s. Run 'gridscraper login %s' first", service, service)
	}

	// Setup browser (visible)
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()

	browserCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	browserCtx, cancel = context.WithTimeout(browserCtx, 10*time.Minute)
	defer cancel()

	// Check if we need to login or use cookies
	var username, password string
	switch service {
	case "nyseg":
		username = cfg.Cookies.NYSEGUsername
		password = cfg.Cookies.NYSEGPassword
	case "coned":
		username = cfg.Cookies.ConEdUsername
		password = cfg.Cookies.ConEdPassword
	}

	// If we have username/password, do automatic login
	if username != "" && password != "" {
		fmt.Println("Performing automatic login...")
		if err := performNYSEGLogin(browserCtx, username, password); err != nil {
			return fmt.Errorf("automatic login failed: %w", err)
		}

		// Extract and save fresh cookies after login
		fmt.Println("Extracting cookies after login...")
		freshCookies, err := scraper.ExtractCookies(browserCtx)
		if err != nil {
			fmt.Printf("Warning: Could not extract cookies: %v\n", err)
		} else {
			switch service {
			case "nyseg":
				cfg.Cookies.NYSEG = freshCookies
			case "coned":
				cfg.Cookies.ConEd = freshCookies
			}
			if err := saveConfig(cfg); err != nil {
				fmt.Printf("Warning: Could not save cookies: %v\n", err)
			} else {
				fmt.Printf("✓ Saved %d fresh cookies\n", len(freshCookies))
			}
		}
	} else {
		// Use existing cookies
		if len(cookies) == 0 {
			return fmt.Errorf("no cookies or credentials found. Add username/password to config or run 'gridscraper login %s'", service)
		}
		if err := scraper.SetCookies(browserCtx, cookies); err != nil {
			return fmt.Errorf("setting cookies: %w", err)
		}
	}

	fmt.Printf("Navigating to %s...\n", loginURL)

	// Enable network domain
	if err := chromedp.Run(browserCtx,
		network.Enable(),
	); err != nil {
		return fmt.Errorf("enabling network: %w", err)
	}

	// Set up request capture
	capturedRequests := make([]map[string]interface{}, 0)

	var capturedAuthToken string

	chromedp.ListenTarget(browserCtx, func(ev interface{}) {
		switch ev := ev.(type) {
		case *network.EventRequestWillBeSent:
			// Capture requests that look like CSV downloads
			url := ev.Request.URL
			if contains(url, "download") || contains(url, "export") || contains(url, "csv") {
				req := map[string]interface{}{
					"url":     url,
					"method":  ev.Request.Method,
					"headers": ev.Request.Headers,
				}

				// Check if there's POST data
				if ev.Request.HasPostData {
					req["hasPostData"] = true
				}

				// Extract Up-Authorization token if present
				if authToken, ok := ev.Request.Headers["Up-Authorization"]; ok {
					if authStr, ok := authToken.(string); ok && authStr != "" {
						capturedAuthToken = authStr
						fmt.Printf("   🔑 Captured auth token\n")
					}
				}

				capturedRequests = append(capturedRequests, req)

				fmt.Printf("\n🎯 Captured request:\n")
				fmt.Printf("   URL: %s\n", url)
				fmt.Printf("   Method: %s\n", ev.Request.Method)
				if ev.Request.HasPostData {
					fmt.Printf("   Has POST Data: true\n")
				}
			}
		}
	})

	// Navigate to the page
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(loginURL),
		chromedp.WaitVisible(`div.engage-insights-explore`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("navigating: %w", err)
	}

	fmt.Println("\n📋 Instructions:")
	fmt.Println("1. Click the 'Download my energy usage data' link")
	fmt.Println("2. Select 'bill period' and 'CSV' options")
	fmt.Println("3. Click the download/export button")
	fmt.Println("4. Press Enter here after the download completes\n")

	fmt.Scanln()

	// Display captured requests
	fmt.Println("\n=== CAPTURED REQUESTS ===")
	if len(capturedRequests) == 0 {
		fmt.Println("No CSV download requests captured.")
		fmt.Println("Make sure you clicked the download button!")
	} else {
		for i, req := range capturedRequests {
			fmt.Printf("\n--- Request #%d ---\n", i+1)
			jsonBytes, _ := json.MarshalIndent(req, "", "  ")
			fmt.Println(string(jsonBytes))
		}
	}
	fmt.Println("=========================\n")

	// Save auth token to config if captured
	if capturedAuthToken != "" {
		fmt.Println("Saving auth token to config...")
		switch service {
		case "nyseg":
			cfg.Cookies.NYSEGAuthToken = capturedAuthToken
		case "coned":
			cfg.Cookies.ConEdAuthToken = capturedAuthToken
		}

		if err := saveConfig(cfg); err != nil {
			fmt.Printf("Warning: Could not save auth token: %v\n", err)
		} else {
			fmt.Printf("✓ Auth token saved to config\n")
		}
	}

	return nil
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 &&
		   (s == substr || len(s) >= len(substr) &&
		   (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
		   indexOf(s, substr) >= 0))
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// performNYSEGLogin performs automatic login to NYSEG
func performNYSEGLogin(ctx context.Context, username, password string) error {
	const loginURL = "https://sso.nyseg.com/es/login"

	return chromedp.Run(ctx,
		chromedp.Navigate(loginURL),
		chromedp.WaitVisible(`input#_com_liferay_login_web_portlet_LoginPortlet_login`, chromedp.ByQuery),
		chromedp.SendKeys(`input#_com_liferay_login_web_portlet_LoginPortlet_login`, username, chromedp.ByQuery),
		chromedp.SendKeys(`input#_com_liferay_login_web_portlet_LoginPortlet_password`, password, chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
		// Submit the form (look for submit button)
		chromedp.Click(`button[type="submit"]`, chromedp.ByQuery),
		chromedp.Sleep(3*time.Second), // Wait for redirect after login
	)
}
