# GridScraper

A Go-based CLI tool to scrape electrical usage data from utility websites (NYSEG and Con Edison). Uses browser automation to extract hourly kWh data and stores it in a local SQLite database. Publishes historical data to Home Assistant for detailed energy tracking and analysis.

## Features

- **Automatic authentication**: Configure username/password for automatic login
- **Hourly usage data**: Fetches detailed hourly kWh readings (24 readings per day)
- **Browser automation**: Uses chromedp for JavaScript-rendered pages
- **Duplicate prevention**: Won't re-scrape existing data
- **SQLite storage**: Local database for tracking usage over time
- **Home Assistant integration**: Publishes historical hourly data via AppDaemon
- **Smart publishing**: Tracks published records to avoid re-uploading
- **Statistics generation**: Automatic compilation for Home Assistant Energy dashboard
- **Automated sync**: Daily cron script for fetch → publish → statistics workflow
- **Debug mode**: Inspect pages to troubleshoot scraping issues

## Installation

```bash
# Clone the repository
git clone https://github.com/jgoulah/gridscraper.git
cd gridscraper

# Build the binary
make build

# Optional: Install to system (installs binary and sync script)
sudo make install
```

See the Makefile for additional targets (`make help`).

## Usage

### 1. Configure Authentication

Add your credentials to `config.yaml`:

```yaml
cookies:
  # NYSEG credentials
  nyseg_username: your-username
  nyseg_password: your-password

  # Con Edison credentials
  coned_username: your-username
  coned_password: your-password
  coned_challenge_answer: "Answer to your security question"
```

The application will automatically log in and refresh authentication as needed when fetching data.

**Alternative: Manual Login** (optional)

If you prefer not to store credentials in the config file, you can manually capture cookies:

```bash
# Login to NYSEG (opens browser for manual login)
gridscraper login nyseg

# Login to Con Edison (opens browser for manual login)
gridscraper login coned
```

This will open a browser window, wait for you to log in, then extract and save the cookies to `config.yaml`.

### 2. Fetch Usage Data

Fetch your hourly usage data:

```bash
# Fetch NYSEG data (last 90 days by default)
gridscraper fetch nyseg

# Fetch Con Edison data
gridscraper fetch coned
```

This will:
- Automatically log in if needed (using credentials or existing cookies)
- Download hourly usage data (NYSEG: API calls, Con Edison: CSV export via browser automation)
- Store hourly readings (24 per day) in `./data.db`
- Skip any timestamps that already exist (duplicate prevention)
- Fetch the last 90 days by default (configurable via `days_to_fetch`, `nyseg_days_to_fetch`, or `coned_days_to_fetch` in config)
- For Con Edison: Downloads 15-minute intervals and aggregates to hourly data

### 3. View Stored Data

List all stored usage data:

```bash
# View all data (all services)
gridscraper list

# View only NYSEG data
gridscraper list --service nyseg

# View only Con Edison data
gridscraper list --service coned
```

Output example:
```
NYSEG Usage Data:
----------------------------------------
Date          kWh
----------------------------------------
2024-11-01      45.23
2024-11-02      52.10
2024-11-03      48.75
----------------------------------------
Total: 146.08 kWh (3 records)
```

### 4. Setup Home Assistant Integration

To publish your historical hourly data to Home Assistant, you need to set up AppDaemon:

#### 4.1. Install AppDaemon Add-on

1. In Home Assistant, go to **Settings → Add-ons → Add-on Store**
2. Search for "AppDaemon" and install it
3. Start the AppDaemon add-on

#### 4.2. Create the Backfill Script

**Find your AppDaemon directory:**

From your Home Assistant host, look for the AppDaemon add-on directory:
```bash
ls -la /addon_configs/ | grep appdaemon
```

It will typically be something like `/addon_configs/a0d7b954_appdaemon/` (where `a0d7b954` is the add-on slug).

**Create the backfill script:**

Copy the template script from `scripts/appdaemon/backfill_state.py` in this repository to `/addon_configs/{addon_slug}_appdaemon/apps/backfill_state.py` on your Home Assistant host.

This script provides two HTTP endpoints:
- `/api/appdaemon/backfill_state` - Stores individual hourly consumption values
- `/api/appdaemon/generate_statistics` - Generates statistics for the Energy dashboard

#### 4.3. Configure the AppDaemon App

Create or edit `/addon_configs/{addon_slug}_appdaemon/apps/apps.yaml`:

```yaml
backfill_state:
  module: backfill_state
  class: BackfillState
```

#### 4.4. Enable Home Assistant Recorder

Ensure the recorder is enabled in your Home Assistant `configuration.yaml`:

```yaml
recorder:
  db_url: sqlite:////config/home-assistant_v2.db
  purge_keep_days: 365  # Keep 1 year of history (adjust as needed)
```

Restart Home Assistant after adding this configuration.

#### 4.5. Restart AppDaemon

Restart the AppDaemon add-on to load the new script. Check the logs to verify you see:
```
Backfill State API endpoints registered:
  - /api/appdaemon/backfill_state
  - /api/appdaemon/generate_statistics
```

#### 4.6. Create Template Sensors

Add template sensors to your Home Assistant `configuration.yaml`:

```yaml
template:
  - sensor:
      # NYSEG sensor
      - name: "NYSEG Energy Usage Direct"
        unique_id: "nyseg_energy_usage_direct"
        unit_of_measurement: 'kWh'
        device_class: energy
        state_class: total_increasing
        state: "{{ float(0) }}"

      # Con Edison sensor
      - name: "ConEd Energy Usage Direct"
        unique_id: "coned_energy_usage_direct"
        unit_of_measurement: 'kWh'
        device_class: energy
        state_class: total_increasing
        state: "{{ float(0) }}"
```

**Important Notes:**
- The `entity_id` is generated from the `name` field (e.g., "ConEd Energy Usage Direct" becomes `sensor.coned_energy_usage_direct`)
- The `entity_id` must match the `entity_id` in your `config.yaml`
- The `state` field is just a placeholder; historical data is backfilled directly to the database
- `state_class: total_increasing` is required for the Energy dashboard

After adding, restart Home Assistant or reload template entities (Developer Tools → YAML → Template Entities).

#### 4.7. Add Sensors to Energy Dashboard

1. Go to Settings → Dashboards → Energy
2. Click "Add Consumption"
3. Select your sensor (e.g., `sensor.nyseg_energy_usage_direct` or `sensor.coned_energy_usage_direct`)
4. Save

This creates the necessary `statistics_meta` entry for the generate-stats command to work.

### 5. Publish to Home Assistant

After fetching data, publish it to Home Assistant:

```bash
# Publish unpublished NYSEG data (default behavior)
gridscraper publish --service nyseg

# Publish to all services (nyseg and coned)
gridscraper publish

# Force republish ALL nyseg data (ignoring published flag)
gridscraper publish --service nyseg --all

# Publish data from the last 7 days
gridscraper publish --service nyseg --since 7d

# Publish specific date range
gridscraper publish --service nyseg --since 2024-01-01 --until 2024-01-31

# Publish limited number of records (for testing)
gridscraper publish --service nyseg --limit 10
```

The publish command:
- By default, only publishes records that haven't been published yet (tracked in local database)
- Use `--all` flag to force republish all records (ignoring published status)
- Sends hourly kWh readings to Home Assistant with proper timestamps
- Marks each record as published after successful upload
- Subsequent runs without `--all` are instant if there's no new data

**Note**: Home Assistant integration must be configured in `config.yaml` first (see Configuration section below).

### 6. Generate Statistics for Energy Dashboard

After publishing data to Home Assistant, you need to generate statistics for the Energy dashboard:

```bash
# Generate statistics for NYSEG (default)
gridscraper generate-stats --service nyseg

# Generate statistics for Con Edison
gridscraper generate-stats --service coned
```

This command:
- Calls the AppDaemon `generate_statistics` endpoint
- Compiles hourly statistics from individual consumption values
- Populates the Home Assistant statistics tables for the Energy dashboard
- Reads entity_id and AppDaemon URL from your `config.yaml`

### 7. Automated Daily Sync

For automated daily data fetching and publishing, use the provided sync script and Makefile:

#### 7.1. Install via Makefile

```bash
# Build and install the binary and sync script
sudo make install
```

This installs:
- `/usr/local/bin/gridscraper` - the main binary
- `/usr/local/bin/gridscraper-sync.sh` - automated sync script (supports nyseg, coned, or both)

#### 7.2. Setup Configuration Files

Copy your configuration and database to the standard location:

```bash
sudo mkdir -p /usr/local/etc/gridscraper
sudo cp config.yaml /usr/local/etc/gridscraper/config.yaml
sudo cp data.db /usr/local/etc/gridscraper/data.db
```

**Important**: Update `days_to_fetch` in your production `config.yaml`:

```yaml
# Global default (fallback: 90)
days_to_fetch: 90

# Service-specific overrides (optional)
nyseg_days_to_fetch: 15    # Fetch last 15 days for NYSEG
coned_days_to_fetch: 365   # Fetch last year for Con Edison
```

#### 7.3. Schedule with Cron

Add to your crontab:

```bash
# Edit crontab
crontab -e

# Option 1: Sync all services (runs daily at 6 AM)
0 6 * * * /usr/local/bin/gridscraper-sync.sh >> /usr/local/etc/gridscraper/sync.log 2>&1

# Option 2: Sync specific services at different times
0 6 * * * /usr/local/bin/gridscraper-sync.sh nyseg >> /usr/local/etc/gridscraper/nyseg.log 2>&1
0 7 * * * /usr/local/bin/gridscraper-sync.sh coned >> /usr/local/etc/gridscraper/coned.log 2>&1
```

The sync script automatically:
1. Fetches new data from the specified utility (or all utilities if no argument)
2. Publishes new records to the appropriate Home Assistant instance
3. Generates statistics for the Energy dashboard

### 8. Debug Scraping Issues

If data extraction fails, use debug mode:

```bash
# Open visible browser to inspect page
gridscraper debug nyseg --visible

# Save HTML to file for inspection
gridscraper debug nyseg --output output.html
```

## Configuration

### Config File Location

Default: `./config.yaml` (in current directory)

Override with: `--config /path/to/config.yaml`

### Database Location

Default: `./data.db` (in current directory)

Override with: `--db /path/to/data.db`

**Note**: Both `config.yaml` and `data.db` are in `.gitignore` to avoid accidentally committing sensitive cookies or personal data.

### Config File Format

```yaml
# Number of days of historical data to fetch (default: 90)
days_to_fetch: 90  # Global default for all services

# Service-specific overrides (optional - falls back to days_to_fetch if not set)
nyseg_days_to_fetch: 90    # NYSEG-specific days to fetch
coned_days_to_fetch: 365   # ConEd-specific days to fetch

cookies:
  # NYSEG credentials (automatic authentication - recommended)
  nyseg_username: your-username
  nyseg_password: your-password
  nyseg: []  # Optional manual cookies (captured via 'login' command)
  nyseg_auth_token: ""  # Automatically populated

  # Con Edison credentials
  coned_username: your-username
  coned_password: your-password
  coned_challenge_answer: "Your security question answer"
  coned: []  # Optional manual cookies
  coned_auth_token: ""  # Automatically populated
  coned_customer_uuid: ""  # Automatically populated

# Home Assistant Configuration (for NYSEG)
home_assistant:
  enabled: true
  url: "http://yourdomain.local:5050"  # AppDaemon port, not HA port (8123)
  token: "your-long-lived-access-token"
  entity_id: "sensor.nyseg_energy_usage_direct"

# Con Edison Home Assistant Configuration (separate instance)
coned_home_assistant:
  enabled: true
  url: "http://yourdomain.local:5050"  # AppDaemon port
  token: "your-long-lived-access-token"
  entity_id: "sensor.coned_energy_usage_direct"
```

**Configuration Options:**
- `days_to_fetch`: Global default for number of days to fetch (default: 90)
- `nyseg_days_to_fetch`: NYSEG-specific override (optional, falls back to `days_to_fetch`)
- `coned_days_to_fetch`: ConEd-specific override (optional, falls back to `days_to_fetch`)

**Authentication:**
- `nyseg_username` / `nyseg_password`: NYSEG credentials for automatic login (recommended)
- `coned_username` / `coned_password` / `coned_challenge_answer`: Con Edison credentials
- Tokens and UUIDs are automatically populated and refreshed by the application

**Home Assistant Configuration:**
- `enabled`: Set to `true` to enable Home Assistant publishing
- `url`: AppDaemon URL with port 5050 (not the Home Assistant port 8123)
- `token`: Long-lived access token from Home Assistant (Settings → Profile → Long-Lived Access Tokens)
- `entity_id`: The sensor entity ID to populate with historical data
- Separate configurations allow publishing to different Home Assistant instances

## Project Structure

```
gridscraper/
├── cmd/gridscraper/           # CLI commands
│   ├── main.go               # Entry point
│   ├── root.go               # Root command & shared logic
│   ├── login.go              # Login command
│   ├── fetch.go              # Fetch command
│   ├── list.go               # List command
│   ├── publish.go            # Publish command (Home Assistant)
│   ├── generate_stats.go     # Generate statistics command
│   └── debug.go              # Debug command
├── internal/
│   ├── config/               # YAML config handling
│   │   └── config.go
│   ├── database/             # SQLite operations
│   │   └── db.go
│   ├── publisher/            # Home Assistant publishing
│   │   └── mqtt.go
│   └── scraper/              # Scraping logic
│       ├── browser.go        # Cookie management
│       └── nyseg.go          # NYSEG scraper
├── pkg/models/               # Data models
│   └── usage.go
├── scripts/
│   ├── appdaemon/            # AppDaemon scripts for Home Assistant
│   │   └── backfill_state.py
│   └── gridscraper-sync.sh   # Automated daily sync script
├── Makefile                  # Build automation
├── go.mod
├── go.sum
└── README.md
```

## Development

### Requirements

- Go 1.24+
- Chrome/Chromium (for headless browser automation)

### Dependencies

- `github.com/chromedp/chromedp` - Browser automation
- `github.com/spf13/cobra` - CLI framework
- `gopkg.in/yaml.v3` - YAML config parsing
- `modernc.org/sqlite` - Pure Go SQLite driver

### Building

```bash
# Build binary
make build

# Build and install to system
sudo make install

# Clean build artifacts
make clean
```

### Running Tests

```bash
go test ./...
```

## Supported Services

### NYSEG (New York State Electric & Gas)

- **Status**: Implemented
- **URL**: https://energymanager.nyseg.com/insights
- **Data**: Hourly kWh usage (24 readings/day)
- **Method**: Direct API calls with browser authentication
- **Home Assistant**: Single instance support

### Con Edison (NYC)

- **Status**: Implemented
- **URL**: https://www.coned.com/en/accounts-billing/my-account/energy-use
- **Data**: 15-minute interval data (96 readings/day), aggregated to hourly
- **Method**: Browser automation with CSV export
- **Authentication**: Username, password, and security challenge question
- **Home Assistant**: Separate instance support (can use different HA server)

## Troubleshooting

### "No cookies found" error

Run the login command first:
```bash
gridscraper login nyseg
```

### Scraper can't extract data

1. Use debug mode to inspect the page:
   ```bash
   gridscraper debug nyseg --visible
   ```

2. Check if the page structure has changed

3. Save HTML and inspect selectors:
   ```bash
   gridscraper debug nyseg --output page.html
   ```

### Cookies expired

Re-run the login command to refresh cookies:
```bash
gridscraper login nyseg
```

## License

MIT

## Contributing

Contributions welcome! Please open an issue or pull request.
