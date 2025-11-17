package main

import (
	"fmt"
	"time"

	"github.com/jgoulah/gridscraper/internal/publisher"
	"github.com/jgoulah/gridscraper/pkg/models"
	"github.com/spf13/cobra"
)

var (
	publishService string
	publishSince   string
	publishUntil   string
	publishAll     bool
	publishLimit   int
)

var publishCmd = &cobra.Command{
	Use:   "publish",
	Short: "Publish usage data to Home Assistant",
	Long:  `Reads stored electrical usage data from the database and publishes it to Home Assistant via HTTP API.`,
	RunE:  runPublish,
}

func init() {
	publishCmd.Flags().StringVar(&publishService, "service", "", "Service to publish (nyseg or coned, default: all services)")
	publishCmd.Flags().StringVar(&publishSince, "since", "", "Only publish data since this date (YYYY-MM-DD or relative like 7d)")
	publishCmd.Flags().StringVar(&publishUntil, "until", "", "Only publish data until this date (YYYY-MM-DD)")
	publishCmd.Flags().BoolVar(&publishAll, "all", false, "Force republish all records (ignore published flag)")
	publishCmd.Flags().IntVar(&publishLimit, "limit", 0, "Limit number of records to publish (0 = no limit)")
	rootCmd.AddCommand(publishCmd)
}

func runPublish(cmd *cobra.Command, args []string) error {
	fmt.Printf("=== Publish started at %s ===\n", time.Now().Format("2006-01-02 15:04:05 MST"))

	// Load config
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Check if Home Assistant is configured
	if !cfg.HomeAssistant.Enabled {
		return fmt.Errorf("Home Assistant is not enabled in config")
	}

	// Create publisher
	pub, err := publisher.New(cfg.HomeAssistant)
	if err != nil {
		return fmt.Errorf("creating publisher: %w", err)
	}

	// Open database
	db, err := openDB()
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	// Determine which services to publish
	services := []string{}
	if publishService != "" {
		services = append(services, publishService)
	} else {
		// If no service specified, publish to all services
		services = append(services, "nyseg", "coned")
	}

	// Parse date filters if provided
	var sinceDate, untilDate *time.Time
	if publishSince != "" {
		since, err := parseDate(publishSince)
		if err != nil {
			return fmt.Errorf("parsing --since date: %w", err)
		}
		sinceDate = &since
	}
	if publishUntil != "" {
		until, err := parseDate(publishUntil)
		if err != nil {
			return fmt.Errorf("parsing --until date: %w", err)
		}
		untilDate = &until
	}

	// Publish data for each service
	totalPublished := 0
	for _, service := range services {
		// Get usage data based on --all flag
		var data []models.UsageData
		if publishAll {
			// When using --all, force republish ALL records
			data, err = db.ListUsage(service)
		} else {
			// Default: only publish unpublished records
			data, err = db.ListUnpublishedUsage(service)
		}
		if err != nil {
			return fmt.Errorf("listing data for %s: %w", service, err)
		}

		if len(data) == 0 {
			if publishAll {
				fmt.Printf("No data found for %s\n", service)
			} else {
				fmt.Printf("No unpublished data found for %s\n", service)
			}
			continue
		}

		// Filter by date range if specified
		filteredData := data
		if sinceDate != nil || untilDate != nil {
			filteredData = []models.UsageData{}
			for _, record := range data {
				if sinceDate != nil && record.Date.Before(*sinceDate) {
					continue
				}
				if untilDate != nil && record.Date.After(*untilDate) {
					continue
				}
				filteredData = append(filteredData, record)
			}
		}

		if len(filteredData) == 0 {
			fmt.Printf("No data in date range for %s\n", service)
			continue
		}

		// Apply limit if specified
		if publishLimit > 0 && len(filteredData) > publishLimit {
			filteredData = filteredData[:publishLimit]
			fmt.Printf("Limiting to %d records (--limit flag)\n", publishLimit)
		}

		// Publish each record
		fmt.Printf("Publishing %d records for %s...\n", len(filteredData), service)
		published := 0
		for i, record := range filteredData {
			fmt.Printf("[%d/%d] Publishing %s (%.2f kWh)... ", i+1, len(filteredData), record.Date.Format("2006-01-02"), record.KWh)
			if err := pub.Publish(record); err != nil {
				fmt.Printf("FAILED: %v\n", err)
				continue
			}

			// Mark record as published in database
			if err := db.MarkPublished(record.ID); err != nil {
				fmt.Printf("✓ (warning: failed to mark as published: %v)\n", err)
			} else {
				fmt.Printf("✓\n")
			}
			published++
		}

		fmt.Printf("Successfully published %d/%d records for %s\n", published, len(filteredData), service)
		totalPublished += published
	}

	fmt.Printf("\nTotal records published: %d\n", totalPublished)
	return nil
}

// parseDate parses a date string in either YYYY-MM-DD format or relative format (e.g., "7d")
func parseDate(dateStr string) (time.Time, error) {
	// Try absolute date format first
	t, err := time.Parse("2006-01-02", dateStr)
	if err == nil {
		return t, nil
	}

	// Try relative format (e.g., "7d" for 7 days ago)
	if len(dateStr) > 1 && dateStr[len(dateStr)-1] == 'd' {
		daysStr := dateStr[:len(dateStr)-1]
		var days int
		if _, err := fmt.Sscanf(daysStr, "%d", &days); err == nil {
			return time.Now().AddDate(0, 0, -days), nil
		}
	}

	return time.Time{}, fmt.Errorf("invalid date format: %s (use YYYY-MM-DD or Nd for N days ago)", dateStr)
}
