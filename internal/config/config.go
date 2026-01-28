package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config holds the application configuration
type Config struct {
	Cookies              CookieConfig `yaml:"cookies"`
	HomeAssistant        HAConfig     `yaml:"home_assistant,omitempty"`
	ConEdHomeAssistant   HAConfig     `yaml:"coned_home_assistant,omitempty"`
	DaysToFetch          int          `yaml:"days_to_fetch,omitempty"`         // Global default (fallback: 90)
	NYSEGDaysToFetch     int          `yaml:"nyseg_days_to_fetch,omitempty"`   // NYSEG-specific override
	ConEdDaysToFetch     int          `yaml:"coned_days_to_fetch,omitempty"`   // ConEd-specific override
	NYSEGRate            float64      `yaml:"nyseg_rate,omitempty"`            // Cost per kWh for NYSEG
	ConEdRate            float64      `yaml:"coned_rate,omitempty"`            // Cost per kWh for ConEd
}

// CookieConfig holds cookies and tokens for different services
type CookieConfig struct {
	NYSEG                []Cookie `yaml:"nyseg"`
	NYSEGAuthToken       string   `yaml:"nyseg_auth_token,omitempty"`
	NYSEGUsername        string   `yaml:"nyseg_username,omitempty"`
	NYSEGPassword        string   `yaml:"nyseg_password,omitempty"`
	ConEd                []Cookie `yaml:"coned"`
	ConEdAuthToken       string   `yaml:"coned_auth_token,omitempty"`
	ConEdCustomerUUID    string   `yaml:"coned_customer_uuid,omitempty"`
	ConEdUsername        string   `yaml:"coned_username,omitempty"`
	ConEdPassword        string   `yaml:"coned_password,omitempty"`
	ConEdChallengeAnswer string   `yaml:"coned_challenge_answer,omitempty"`
}

// Cookie represents a browser cookie
type Cookie struct {
	Name     string  `yaml:"name"`
	Value    string  `yaml:"value"`
	Domain   string  `yaml:"domain"`
	Path     string  `yaml:"path"`
	Expires  float64 `yaml:"expires,omitempty"`
	HTTPOnly bool    `yaml:"httpOnly,omitempty"`
	Secure   bool    `yaml:"secure,omitempty"`
	SameSite string  `yaml:"sameSite,omitempty"`
}

// HAConfig holds Home Assistant HTTP API configuration
type HAConfig struct {
	Enabled  bool   `yaml:"enabled"`
	URL      string `yaml:"url"`               // e.g., "http://yourdomain.local:5050"
	Token    string `yaml:"token"`             // Long-lived access token
	EntityID string `yaml:"entity_id"`         // e.g., "sensor.nyseg_energy_usage_direct"
}

// Load reads the config file
func Load(configPath string) (*Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Return empty config if file doesn't exist
			return &Config{}, nil
		}
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	return &cfg, nil
}

// Save writes the config to file
func Save(configPath string, cfg *Config) error {
	// Ensure directory exists
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	return nil
}

// DefaultConfigPath returns the default config file path (local directory)
func DefaultConfigPath() string {
	return "config.yaml"
}

// GetDaysToFetch returns the number of days to fetch with a default of 90 (3 months)
func (c *Config) GetDaysToFetch() int {
	if c.DaysToFetch <= 0 {
		return 90 // Default to 3 months
	}
	return c.DaysToFetch
}

// GetNYSEGDaysToFetch returns NYSEG-specific days to fetch, falling back to global setting
func (c *Config) GetNYSEGDaysToFetch() int {
	if c.NYSEGDaysToFetch > 0 {
		return c.NYSEGDaysToFetch
	}
	return c.GetDaysToFetch()
}

// GetConEdDaysToFetch returns ConEd-specific days to fetch, falling back to global setting
func (c *Config) GetConEdDaysToFetch() int {
	if c.ConEdDaysToFetch > 0 {
		return c.ConEdDaysToFetch
	}
	return c.GetDaysToFetch()
}

// GetNYSEGRate returns the NYSEG cost per kWh rate, or 0 if not set
func (c *Config) GetNYSEGRate() float64 {
	return c.NYSEGRate
}

// GetConEdRate returns the ConEd cost per kWh rate, or 0 if not set
func (c *Config) GetConEdRate() float64 {
	return c.ConEdRate
}

// GetRate returns the rate for the specified service, or 0 if not set
func (c *Config) GetRate(service string) float64 {
	switch service {
	case "nyseg":
		return c.GetNYSEGRate()
	case "coned":
		return c.GetConEdRate()
	default:
		return 0
	}
}
