#!/bin/bash
# GridScraper sync script - fetch, publish, and generate statistics
# Usage: gridscraper-sync.sh [--regen-stats] [service]
#   --regen-stats: only run generate-stats with --clear-existing (skip fetch/publish)
#   service: nyseg, coned, or omit for all services

CONFIG_FILE="${CONFIG_FILE:-/usr/local/etc/gridscraper/config.yaml}"
DB_FILE="${DB_FILE:-/usr/local/etc/gridscraper/data.db}"
GRIDSCRAPER="/opt/gridscraper/gridscraper"

# Parse flags
REGEN_STATS=false
while [[ "$1" == --* ]]; do
    case "$1" in
        --regen-stats) REGEN_STATS=true ;;
        *) echo "Error: Unknown flag '$1'"; echo "Usage: $0 [--regen-stats] [nyseg|coned|all]"; exit 1 ;;
    esac
    shift
done

# Determine which services to sync
SERVICE="${1:-all}"

# Track results
declare -A RESULTS

# Function to regenerate statistics with --clear-existing for a single service
regen_stats_service() {
    local service=$1
    echo "-----------------------------------------"
    echo "-------- Regenerating stats: $service -------"
    echo "-----------------------------------------"

    echo "Regenerating statistics with --clear-existing for $service..."
    if ! $GRIDSCRAPER --config "$CONFIG_FILE" --db "$DB_FILE" generate-stats --service "$service" --clear-existing; then
        echo "✗ Failed to regenerate statistics for $service"
        RESULTS[$service]="failed"
        echo ""
        return 1
    fi

    echo "✓ $service stats regenerated"
    RESULTS[$service]="success"
    echo ""
    return 0
}

# Function to sync a single service
sync_service() {
    local service=$1
    echo "-----------------------------------------"
    echo "------------- Syncing $service -------------"
    echo "-----------------------------------------"

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

echo "=================================================================================="
echo "=== GridScraper Sync started at $(date) ==="
echo "=================================================================================="
echo ""

RUN_SERVICE=$( $REGEN_STATS && echo "regen_stats_service" || echo "sync_service" )

case "$SERVICE" in
    nyseg)
        $RUN_SERVICE nyseg
        ;;
    coned)
        $RUN_SERVICE coned
        ;;
    all)
        $RUN_SERVICE nyseg
        $RUN_SERVICE coned
        ;;
    *)
        echo "Error: Unknown service '$SERVICE'"
        echo "Usage: $0 [--regen-stats] [nyseg|coned|all]"
        exit 1
        ;;
esac

echo ""
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
echo "=================================================================================="
echo "=================================================================================="
echo ""
echo ""
