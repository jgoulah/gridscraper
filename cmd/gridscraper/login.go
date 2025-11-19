package main

import (
	"context"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/jgoulah/gridscraper/internal/scraper"
	"github.com/spf13/cobra"
)

var loginCmd = &cobra.Command{
	Use:   "login [service]",
	Short: "Login to a service and save cookies",
	Long: `Opens a browser window for you to login manually.
After successful login, cookies will be extracted and saved to the config file.

Available services: nyseg, coned`,
	Args: cobra.ExactArgs(1),
	RunE: runLogin,
}

func init() {
	rootCmd.AddCommand(loginCmd)
}

func runLogin(cmd *cobra.Command, args []string) error {
	service := args[0]

	var loginURL string
	switch service {
	case "nyseg":
		loginURL = "https://energymanager.nyseg.com/insights"
	case "coned":
		loginURL = "https://www.coned.com/en/login"
	default:
		return fmt.Errorf("unknown service: %s (available: nyseg, coned)", service)
	}

	fmt.Printf("Opening browser for %s login...\n", service)
	fmt.Println("Please log in manually in the browser window.")
	fmt.Println("After login, click any download/export button to capture the auth token.")
	fmt.Println("Then press Enter here to save...")

	// Create a visible browser context
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	// Set a longer timeout for user to login
	ctx, cancel = context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	// Enable network monitoring to capture auth token
	var capturedAuthToken string
	var tokenCaptured bool
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		switch ev := ev.(type) {
		case *network.EventRequestWillBeSent:
			// Capture auth token from any request (only report once)
			if !tokenCaptured {
				if authToken, ok := ev.Request.Headers["Up-Authorization"]; ok {
					if authStr, ok := authToken.(string); ok && authStr != "" {
						capturedAuthToken = authStr
						tokenCaptured = true
						fmt.Printf("✓ Captured auth token from network request\n")
					}
				}
			}
		}
	})

	// Navigate to the login page
	if err := chromedp.Run(ctx,
		network.Enable(),
		chromedp.Navigate(loginURL),
	); err != nil {
		return fmt.Errorf("navigating to login page: %w", err)
	}

	// Wait for user to press Enter
	fmt.Scanln()

	// Extract cookies
	fmt.Println("Extracting cookies...")
	cookies, err := scraper.ExtractCookies(ctx)
	if err != nil {
		return fmt.Errorf("extracting cookies: %w", err)
	}

	if len(cookies) == 0 {
		return fmt.Errorf("no cookies found - make sure you're logged in")
	}

	if capturedAuthToken == "" {
		fmt.Println("⚠ Warning: No auth token captured from network requests")
		fmt.Println("  You may need to click a download/export button, or add username/password to config for auto-login")
	}

	// Load existing config
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Update cookies for the service
	switch service {
	case "nyseg":
		cfg.Cookies.NYSEG = cookies
		if capturedAuthToken != "" {
			cfg.Cookies.NYSEGAuthToken = capturedAuthToken
		}
	case "coned":
		cfg.Cookies.ConEd = cookies
	}

	// Save config
	if err := saveConfig(cfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	if service == "nyseg" && capturedAuthToken != "" {
		fmt.Printf("✓ Successfully saved %d cookies and auth token for %s\n", len(cookies), service)
	} else {
		fmt.Printf("✓ Successfully saved %d cookies for %s\n", len(cookies), service)
		if service == "nyseg" && capturedAuthToken == "" {
			fmt.Println("  ⚠ No auth token captured - click a download button or add username/password to config")
		}
	}
	return nil
}
