#!/bin/bash
# GridScraper sync script - fetch, publish, and generate statistics
# Usage: gridscraper-sync.sh [service]
#   service: nyseg, coned, or omit for all services

CONFIG_FILE="${CONFIG_FILE:-/usr/local/etc/gridscraper/config.yaml}"
DB_FILE="${DB_FILE:-/usr/local/etc/gridscraper/data.db}"
GRIDSCRAPER="/opt/gridscraper/gridscraper"

# Determine which services to sync
SERVICE="${1:-all}"

# Track results
declare -A RESULTS

# Function to sync a single service
sync_service() {
    local service=$1
    echo "--- Syncing $service ---"

    # Step 1: Fetch data
    echo "Step 1: Fetching data from $service..."
    if ! $GRIDSCRAPER --config "$CONFIG_FILE" --db "$DB_FILE" fetch "$service"; then
        echo "✗ Failed to fetch data from $service"
        RESULTS[$service]="failed"
        echo ""
        return 1
    fi

    # Step 2: Publish to Home Assistant
    echo "Step 2: Publishing to Home Assistant..."
    if ! $GRIDSCRAPER --config "$CONFIG_FILE" --db "$DB_FILE" publish --service "$service"; then
        echo "✗ Failed to publish $service to Home Assistant"
        RESULTS[$service]="failed"
        echo ""
        return 1
    fi

    # Step 3: Generate statistics
    echo "Step 3: Generating statistics..."
    if ! $GRIDSCRAPER --config "$CONFIG_FILE" --db "$DB_FILE" generate-stats --service "$service"; then
        echo "✗ Failed to generate statistics for $service"
        RESULTS[$service]="failed"
        echo ""
        return 1
    fi

    echo "✓ $service sync completed"
    RESULTS[$service]="success"
    echo ""
    return 0
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
echo ""
echo "Summary:"
for service in "${!RESULTS[@]}"; do
    if [ "${RESULTS[$service]}" = "success" ]; then
        echo "  ✓ $service: success"
    else
        echo "  ✗ $service: failed"
    fi
done
