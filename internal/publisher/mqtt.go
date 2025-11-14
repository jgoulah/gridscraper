package publisher

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jgoulah/gridscraper/internal/config"
	"github.com/jgoulah/gridscraper/pkg/models"
)

// Publisher handles publishing to Home Assistant
type Publisher struct {
	haConfig config.HAConfig
}

// New creates a new publisher for Home Assistant
func New(haCfg config.HAConfig) (*Publisher, error) {
	// Validate HA config if enabled
	if haCfg.Enabled {
		if haCfg.URL == "" {
			return nil, fmt.Errorf("Home Assistant URL is required when enabled")
		}
		if haCfg.Token == "" {
			return nil, fmt.Errorf("Home Assistant token is required when enabled")
		}
		if haCfg.EntityID == "" {
			return nil, fmt.Errorf("Home Assistant entity_id is required when enabled")
		}
	}

	return &Publisher{
		haConfig: haCfg,
	}, nil
}

// HAPayload matches the Home Assistant backfill service call data
type HAPayload struct {
	EntityID    string `json:"entity_id"`
	State       string `json:"state"`
	LastChanged string `json:"last_changed"`
	LastUpdated string `json:"last_updated"`
}

// Publish sends a usage reading to Home Assistant via HTTP API
func (p *Publisher) Publish(reading models.UsageData) error {
	if !p.haConfig.Enabled {
		return fmt.Errorf("Home Assistant publishing is not enabled in config")
	}

	// Build the full API URL (AppDaemon API endpoint)
	apiURL := fmt.Sprintf("%s/api/appdaemon/backfill_state", p.haConfig.URL)

	// Determine timestamp to use for last_changed and last_updated
	var timestamp string
	if !reading.StartTime.IsZero() {
		timestamp = reading.StartTime.Format(time.RFC3339)
	} else {
		timestamp = reading.Date.Format(time.RFC3339)
	}

	// Create payload for Home Assistant
	payload := HAPayload{
		EntityID:    p.haConfig.EntityID,
		State:       fmt.Sprintf("%.2f", reading.KWh),
		LastChanged: timestamp,
		LastUpdated: timestamp,
	}

	// Marshal to JSON
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encoding payload: %w", err)
	}

	// Create HTTP request
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.haConfig.Token)
	req.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Read error response body for debugging
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP error: status %d, response: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

