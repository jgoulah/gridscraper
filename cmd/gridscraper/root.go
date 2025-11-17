package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jgoulah/gridscraper/internal/config"
	"github.com/jgoulah/gridscraper/internal/database"
	"github.com/spf13/cobra"
)

var (
	cfgFile string
	dbPath  string
)

var rootCmd = &cobra.Command{
	Use:   "gridscraper",
	Short: "Scrape electrical usage data from NYSEG and Con Edison",
	Long: `GridScraper is a CLI tool to collect electrical usage data from utility websites.
It uses browser automation to extract daily kWh data and stores it in a local SQLite database.`,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is ./config.yaml)")
	rootCmd.PersistentFlags().StringVar(&dbPath, "db", "", "database file (default is ./data.db)")
}

// getConfigPath returns the config file path
func getConfigPath() string {
	if cfgFile != "" {
		return cfgFile
	}
	return config.DefaultConfigPath()
}

// getDBPath returns the database file path (local directory)
func getDBPath() string {
	if dbPath != "" {
		return dbPath
	}
	return "data.db"
}

// loadConfig loads the configuration file
func loadConfig() (*config.Config, error) {
	return config.Load(getConfigPath())
}

// saveConfig saves the configuration file
func saveConfig(cfg *config.Config) error {
	return config.Save(getConfigPath(), cfg)
}

// openDB opens the database connection
func openDB() (*database.DB, error) {
	path := getDBPath()

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating database directory: %w", err)
	}

	return database.New(path)
}
