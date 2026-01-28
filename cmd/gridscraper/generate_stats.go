package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jgoulah/gridscraper/internal/config"
	"github.com/spf13/cobra"
)

var (
	generateStatsService string
	generateStatsRate    string
)

var generateStatsCmd = &cobra.Command{
	Use:   "generate-stats",
	Short: "Generate statistics in Home Assistant from backfilled states",
	Long:  `Calls AppDaemon endpoint to compile statistics from individual hourly consumption states. Run this after publishing to populate the Energy dashboard.`,
	RunE:  runGenerateStats,
}

func init() {
	generateStatsCmd.Flags().StringVar(&generateStatsService, "service", "nyseg", "Service to generate stats for (nyseg or coned, default: nyseg)")
	generateStatsCmd.Flags().StringVar(&generateStatsRate, "rate", "", "Optional cost per kWh rate for cost statistics (e.g., 0.20102749)")
	rootCmd.AddCommand(generateStatsCmd)
}

func runGenerateStats(cmd *cobra.Command, args []string) error {
	fmt.Printf("=== Generate Statistics started at %s ===\n", time.Now().Format("2006-01-02 15:04:05 MST"))

	// Load config
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Select the appropriate Home Assistant config based on service
	var haConfig config.HAConfig
	switch generateStatsService {
	case "nyseg":
		haConfig = cfg.HomeAssistant
	case "coned":
		haConfig = cfg.ConEdHomeAssistant
	default:
		return fmt.Errorf("unknown service: %s (available: nyseg, coned)", generateStatsService)
	}

	// Check if Home Assistant is configured for this service
	if !haConfig.Enabled {
		return fmt.Errorf("Home Assistant is not enabled for %s in config", generateStatsService)
	}

	// Show which HA instance we're working with
	fmt.Printf("Generating statistics for %s at Home Assistant: %s (entity: %s)\n", generateStatsService, haConfig.URL, haConfig.EntityID)

	// Build API URL
	apiURL := fmt.Sprintf("%s/api/appdaemon/generate_statistics", haConfig.URL)

	// Create payload
	payload := map[string]string{
		"entity_id": haConfig.EntityID,
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

	req.Header.Set("Authorization", "Bearer "+haConfig.Token)
	req.Header.Set("Content-Type", "application/json")

	fmt.Printf("Generating statistics for %s (%s)...\n", haConfig.EntityID, generateStatsService)

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

	// Generate cost statistics
	fmt.Printf("\nGenerating cost statistics for %s...\n", haConfig.EntityID)

	costEntityID := haConfig.EntityID + "_cost"
	costAPIURL := fmt.Sprintf("%s/api/appdaemon/generate_cost_statistics", haConfig.URL)

	costPayload := map[string]string{
		"energy_entity_id": haConfig.EntityID,
		"cost_entity_id":   costEntityID,
	}

	// Add rate if provided via flag or config
	if generateStatsRate != "" {
		costPayload["rate"] = generateStatsRate
		fmt.Printf("Using provided rate: %s per kWh\n", generateStatsRate)
	} else if configRate := cfg.GetRate(generateStatsService); configRate > 0 {
		costPayload["rate"] = fmt.Sprintf("%f", configRate)
		fmt.Printf("Using config rate: $%.6f per kWh\n", configRate)
	}

	costBody, err := json.Marshal(costPayload)
	if err != nil {
		return fmt.Errorf("encoding cost payload: %w", err)
	}

	costReq, err := http.NewRequest("POST", costAPIURL, bytes.NewBuffer(costBody))
	if err != nil {
		return fmt.Errorf("creating cost request: %w", err)
	}

	costReq.Header.Set("Authorization", "Bearer "+haConfig.Token)
	costReq.Header.Set("Content-Type", "application/json")

	costResp, err := client.Do(costReq)
	if err != nil {
		return fmt.Errorf("cost request error: %w", err)
	}
	defer costResp.Body.Close()

	costRespBody, _ := io.ReadAll(costResp.Body)

	if costResp.StatusCode != http.StatusOK {
		return fmt.Errorf("cost HTTP error: status %d, response: %s", costResp.StatusCode, string(costRespBody))
	}

	var costResult map[string]interface{}
	if err := json.Unmarshal(costRespBody, &costResult); err != nil {
		return fmt.Errorf("parsing cost response: %w", err)
	}

	fmt.Printf("✓ Cost statistics generated successfully\n")
	if inserted, ok := costResult["inserted"].(float64); ok {
		fmt.Printf("  - Inserted: %d new cost records\n", int(inserted))
	}
	if updated, ok := costResult["updated"].(float64); ok {
		fmt.Printf("  - Updated: %d existing cost records\n", int(updated))
	}
	if totalCost, ok := costResult["total_cost"].(float64); ok {
		fmt.Printf("  - Total cost: $%.2f\n", totalCost)
	}
	if rateUsed, ok := costResult["rate_used"].(float64); ok {
		fmt.Printf("  - Rate used: $%.5f/kWh\n", rateUsed)
	}

	return nil
}
