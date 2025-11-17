#!/bin/bash
# GridScraper sync script - fetch, publish, and generate statistics

set -e  # Exit on error

CONFIG_FILE="${CONFIG_FILE:-/usr/local/etc/gridscraper/config.yaml}"
DB_FILE="${DB_FILE:-/usr/local/etc/gridscraper/data.db}"
GRIDSCRAPER="/usr/local/bin/gridscraper"

echo "=== GridScraper Sync started at $(date) ==="

# Step 1: Fetch data
echo "Fetching data from NYSEG..."
$GRIDSCRAPER --config "$CONFIG_FILE" --db "$DB_FILE" fetch nyseg

# Step 2: Publish to Home Assistant
echo "Publishing to Home Assistant..."
$GRIDSCRAPER --config "$CONFIG_FILE" --db "$DB_FILE" publish --service nyseg

# Step 3: Generate statistics
$GRIDSCRAPER --config "$CONFIG_FILE" --db "$DB_FILE" generate-stats

echo "=== GridScraper Sync completed at $(date) ==="
