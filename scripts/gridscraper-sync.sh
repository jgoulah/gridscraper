#!/bin/bash
# GridScraper sync script - fetch, publish, and generate statistics
# Usage: gridscraper-sync.sh [service]
#   service: nyseg, coned, or omit for all services

set -e  # Exit on error

CONFIG_FILE="${CONFIG_FILE:-/usr/local/etc/gridscraper/config.yaml}"
DB_FILE="${DB_FILE:-/usr/local/etc/gridscraper/data.db}"
GRIDSCRAPER="/usr/local/bin/gridscraper"

# Determine which services to sync
SERVICE="${1:-all}"

# Function to sync a single service
sync_service() {
    local service=$1
    echo "--- Syncing $service ---"

    # Step 1: Fetch data
    echo "Step 1: Fetching data from $service..."
    $GRIDSCRAPER --config "$CONFIG_FILE" --db "$DB_FILE" fetch "$service"

    # Step 2: Publish to Home Assistant
    echo "Step 2: Publishing to Home Assistant..."
    $GRIDSCRAPER --config "$CONFIG_FILE" --db "$DB_FILE" publish --service "$service"

    # Step 3: Generate statistics
    echo "Step 3: Generating statistics..."
    $GRIDSCRAPER --config "$CONFIG_FILE" --db "$DB_FILE" generate-stats --service "$service"

    echo "✓ $service sync completed"
    echo ""
}

echo "=== GridScraper Sync started at $(date) ==="

case "$SERVICE" in
    nyseg)
        sync_service nyseg
        ;;
    coned)
        sync_service coned
        ;;
    all)
        sync_service nyseg
        sync_service coned
        ;;
    *)
        echo "Error: Unknown service '$SERVICE'"
        echo "Usage: $0 [nyseg|coned|all]"
        exit 1
        ;;
esac

echo "=== GridScraper Sync completed at $(date) ==="
