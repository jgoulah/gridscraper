package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

var generateStatsCmd = &cobra.Command{
	Use:   "generate-stats",
	Short: "Generate statistics in Home Assistant from backfilled states",
	Long:  `Calls AppDaemon endpoint to compile statistics from individual hourly consumption states. Run this after publishing to populate the Energy dashboard.`,
	RunE:  runGenerateStats,
}

func init() {
	rootCmd.AddCommand(generateStatsCmd)
}

func runGenerateStats(cmd *cobra.Command, args []string) error {
	fmt.Printf("=== Generate Statistics started at %s ===\n", time.Now().Format("2006-01-02 15:04:05 MST"))

	// Load config
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Check if Home Assistant is configured
	if !cfg.HomeAssistant.Enabled {
		return fmt.Errorf("Home Assistant is not enabled in config")
	}

	// Build API URL
	apiURL := fmt.Sprintf("%s/api/appdaemon/generate_statistics", cfg.HomeAssistant.URL)

	// Create payload
	payload := map[string]string{
		"entity_id": cfg.HomeAssistant.EntityID,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encoding payload: %w", err)
	}

	// Create HTTP request
	client := &http.Client{Timeout: 60 * time.Second} // Longer timeout for statistics generation
	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+cfg.HomeAssistant.Token)
	req.Header.Set("Content-Type", "application/json")

	fmt.Printf("Generating statistics for %s...\n", cfg.HomeAssistant.EntityID)

	// Send request
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request error: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP error: status %d, response: %s", resp.StatusCode, string(respBody))
	}

	// Parse response
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("parsing response: %w", err)
	}

	// Display results
	fmt.Printf("✓ Statistics generated successfully\n")
	if inserted, ok := result["inserted"].(float64); ok {
		fmt.Printf("  - Inserted: %d new statistics records\n", int(inserted))
	}
	if updated, ok := result["updated"].(float64); ok {
		fmt.Printf("  - Updated: %d existing statistics records\n", int(updated))
	}
	if totalHours, ok := result["total_hours"].(float64); ok {
		fmt.Printf("  - Total hours: %d\n", int(totalHours))
	}

	return nil
}
