package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jgoulah/gridscraper/internal/config"
	"github.com/jgoulah/gridscraper/internal/scraper"
	"github.com/spf13/cobra"
)

var fetchVisible bool

var fetchCmd = &cobra.Command{
	Use:   "fetch [service]",
	Short: "Fetch usage data from a service",
	Long: `Scrapes electrical usage data from the specified service using saved cookies.
Data will be stored in the local SQLite database.

Available services: nyseg, coned`,
	Args: cobra.ExactArgs(1),
	RunE: runFetch,
}

func init() {
	fetchCmd.Flags().BoolVar(&fetchVisible, "visible", false, "Show browser window (for debugging)")
	rootCmd.AddCommand(fetchCmd)
}

func runFetch(cmd *cobra.Command, args []string) error {
	fmt.Printf("=== Fetch started at %s ===\n", time.Now().Format("2006-01-02 15:04:05 MST"))

	service := args[0]

	// Validate service
	if service != "nyseg" && service != "coned" {
		return fmt.Errorf("unknown service: %s (available: nyseg, coned)", service)
	}

	if service == "coned" {
		return fmt.Errorf("Con Edison support not yet implemented")
	}

	// Load config
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Get cookies, auth token, and credentials for service
	var cookies []config.Cookie
	var authToken, username, password string
	switch service {
	case "nyseg":
		cookies = cfg.Cookies.NYSEG
		authToken = cfg.Cookies.NYSEGAuthToken
		username = cfg.Cookies.NYSEGUsername
		password = cfg.Cookies.NYSEGPassword
	case "coned":
		cookies = cfg.Cookies.ConEd
		authToken = cfg.Cookies.ConEdAuthToken
		username = cfg.Cookies.ConEdUsername
		password = cfg.Cookies.ConEdPassword
	}

	// Check if we have either cookies+token OR username+password for auto-auth
	if len(cookies) == 0 && (username == "" || password == "") {
		return fmt.Errorf("no authentication configured for %s. Add username/password to config.yaml or run 'gridscraper login %s'", service, service)
	}

	// Open database
	db, err := openDB()
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	// Create scraper with credentials for auto-auth
	var nysegScraper *scraper.NYSEGDirectScraper
	switch service {
	case "nyseg":
		nysegScraper = scraper.NewNYSEGDirectScraperWithCredentials(cookies, authToken, username, password)
	default:
		return fmt.Errorf("scraper not implemented for %s", service)
	}

	// If we have username/password but no cookies, do proactive login
	ctx := context.Background()
	if len(cookies) == 0 && username != "" && password != "" {
		fmt.Println("No cookies found, performing initial login...")
		freshCookies, freshToken, err := nysegScraper.RefreshAuth(ctx)
		if err != nil {
			return fmt.Errorf("initial login failed: %w", err)
		}

		// Save credentials
		switch service {
		case "nyseg":
			cfg.Cookies.NYSEG = freshCookies
			cfg.Cookies.NYSEGAuthToken = freshToken
		case "coned":
			cfg.Cookies.ConEd = freshCookies
			cfg.Cookies.ConEdAuthToken = freshToken
		}

		if err := saveConfig(cfg); err != nil {
			fmt.Printf("Warning: Could not save credentials: %v\n", err)
		} else {
			fmt.Println("✓ Login successful, credentials saved")
		}
	}

	// Scrape data with automatic auth refresh on failure
	daysToFetch := cfg.GetDaysToFetch()
	fmt.Printf("Fetching data from %s (last %d days)...\n", service, daysToFetch)
	data, err := nysegScraper.Scrape(ctx, daysToFetch)

	// If scraping failed and we have credentials, try refreshing auth and retry
	// This handles auth errors, expired tokens, and protocol errors from bad auth
	if err != nil && username != "" && password != "" {
		fmt.Printf("⚠ Scraping failed: %v\n", err)
		fmt.Printf("⚠ Attempting to refresh credentials and retry...\n")

		freshCookies, freshToken, refreshErr := nysegScraper.RefreshAuth(ctx)
		if refreshErr != nil {
			return fmt.Errorf("refreshing auth: %w (original error: %v)", refreshErr, err)
		}

		// Save refreshed credentials
		switch service {
		case "nyseg":
			cfg.Cookies.NYSEG = freshCookies
			cfg.Cookies.NYSEGAuthToken = freshToken
		case "coned":
			cfg.Cookies.ConEd = freshCookies
			cfg.Cookies.ConEdAuthToken = freshToken
		}

		if saveErr := saveConfig(cfg); saveErr != nil {
			fmt.Printf("Warning: Could not save refreshed credentials: %v\n", saveErr)
		} else {
			fmt.Println("✓ Credentials refreshed and saved")
		}

		// Retry scrape with fresh credentials
		fmt.Println("Retrying fetch with fresh credentials...")
		data, err = nysegScraper.Scrape(ctx, daysToFetch)

		if err != nil {
			return fmt.Errorf("scraping failed after auth refresh: %w", err)
		}
	} else if err != nil {
		// No credentials to retry with
		return fmt.Errorf("scraping: %w (hint: add username/password to config.yaml for automatic login)", err)
	}

	if len(data) == 0 {
		fmt.Println("No data found")
		return nil
	}

	// Store data (duplicates will be ignored by UNIQUE constraint)
	totalRecords := 0

	for _, record := range data {
		// Set service name
		record.Service = service

		// Insert new data (INSERT OR IGNORE will skip duplicates based on UNIQUE constraint)
		if err := db.InsertUsage(&record); err != nil {
			return fmt.Errorf("inserting usage data: %w", err)
		}

		totalRecords++
	}

	fmt.Printf("✓ Processed %d records (duplicates automatically skipped by database)\n", totalRecords)
	return nil
}
