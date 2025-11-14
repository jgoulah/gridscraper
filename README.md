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
- **Debug mode**: Inspect pages to troubleshoot scraping issues

## Installation

```bash
# Clone the repository
git clone https://github.com/jgoulah/gridscraper.git
cd gridscraper

# Build the binary
go build -o gridscraper ./cmd/gridscraper

# Optional: Install to your PATH
sudo mv gridscraper /usr/local/bin/
```

## Usage

### 1. Configure Authentication

Add your NYSEG credentials to `config.yaml`:

```yaml
cookies:
  nyseg_username: your-username
  nyseg_password: your-password
```

The application will automatically log in and refresh authentication as needed when fetching data.

**Alternative: Manual Login** (optional)

If you prefer not to store credentials in the config file, you can manually capture cookies:

```bash
# Login to NYSEG (opens browser for manual login)
gridscraper login nyseg
```

This will open a browser window, wait for you to log in, then extract and save the cookies to `config.yaml`.

### 2. Fetch Usage Data

Fetch your hourly usage data:

```bash
# Fetch NYSEG data (last 90 days by default)
gridscraper fetch nyseg
```

This will:
- Automatically log in if needed (using credentials or existing cookies)
- Download hourly usage data via the NYSEG API
- Store hourly readings (24 per day) in `./data.db`
- Skip any timestamps that already exist (duplicate prevention)
- Fetch the last 90 days by default (configurable via `days_to_fetch` in config)

### 3. View Stored Data

List all stored usage data:

```bash
# View all data
gridscraper list

# View only NYSEG data
gridscraper list --service nyseg
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

Create the file `/addon_configs/{addon_slug}_appdaemon/apps/backfill_state.py` with the following content:

```python
import hassapi as hass
import sqlite3
from datetime import datetime

class BackfillState(hass.Hass):
    def initialize(self):
        # Register HTTP API endpoint
        self.register_endpoint(self.api_call, "backfill_state")
        self.log("Backfill State API endpoint registered at /api/appdaemon/backfill_state")

    def api_call(self, data, **kwargs):
        """Handle HTTP API calls"""
        entity_id = data.get("entity_id")
        state = data.get("state")
        last_changed = data.get("last_changed")
        last_updated = data.get("last_updated", last_changed)

        self.log(f"Backfill request: {entity_id} = {state} at {last_changed}")

        if not entity_id or not state or not last_changed:
            self.log("Missing required parameters", level="WARNING")
            return {"error": "Missing required parameters"}, 400

        try:
            conn = sqlite3.connect('/homeassistant/home-assistant_v2.db', timeout=30)
            cursor = conn.cursor()

            last_changed_dt = datetime.fromisoformat(last_changed.replace('Z', '+00:00'))
            last_updated_dt = datetime.fromisoformat(last_updated.replace('Z', '+00:00'))

            # Get or create metadata_id
            cursor.execute("""
                SELECT metadata_id FROM states_meta
                WHERE entity_id = ?
            """, (entity_id,))

            row = cursor.fetchone()
            if row:
                metadata_id = row[0]
            else:
                cursor.execute("""
                    INSERT INTO states_meta (entity_id)
                    VALUES (?)
                """, (entity_id,))
                metadata_id = cursor.lastrowid

            # Check for duplicates
            cursor.execute("""
                SELECT state_id FROM states
                WHERE metadata_id = ?
                AND last_changed_ts = ?
            """, (metadata_id, last_changed_dt.timestamp()))

            if cursor.fetchone():
                self.log(f"State already exists for {entity_id} at {last_changed}, skipping")
                conn.close()
                return {"status": "skipped", "reason": "duplicate"}, 200
            else:
                # Insert new state
                cursor.execute("""
                    INSERT INTO states (
                        metadata_id, state, last_changed_ts, last_updated_ts
                    )
                    VALUES (?, ?, ?, ?)
                """, (
                    metadata_id,
                    state,
                    last_changed_dt.timestamp(),
                    last_updated_dt.timestamp()
                ))

                self.log(f"Inserted state for {entity_id}: {state} at {last_changed}")

            conn.commit()
            conn.close()

            return {"status": "success"}, 200

        except Exception as e:
            self.log(f"Database error: {str(e)}", level="ERROR")
            if 'conn' in locals():
                conn.close()
            return {"error": str(e)}, 500
```

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
Backfill State API endpoint registered at /api/appdaemon/backfill_state
```

### 5. Publish to Home Assistant

After fetching data, publish it to Home Assistant:

```bash
# Publish all unpublished NYSEG data
gridscraper publish --service nyseg --all

# Publish data from the last 7 days
gridscraper publish --service nyseg --since 7d

# Publish specific date range
gridscraper publish --service nyseg --since 2024-01-01 --until 2024-01-31

# Publish limited number of records (for testing)
gridscraper publish --service nyseg --limit 10
```

The publish command:
- Only publishes records that haven't been published yet (tracked in local database)
- Sends hourly kWh readings to Home Assistant with proper timestamps
- Marks each record as published after successful upload
- Subsequent `--all` runs are instant if there's no new data

**Note**: Home Assistant integration must be configured in `config.yaml` first (see Configuration section below).

### 5. Debug Scraping Issues

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
# Number of days of historical data to fetch from API (default: 90)
days_to_fetch: 90

cookies:
  # Automatic authentication (recommended)
  nyseg_username: your-username
  nyseg_password: your-password

  # Or manual cookie-based auth (optional)
  nyseg:
    - name: session_id
      value: abc123...
      domain: energymanager.nyseg.com
      path: /
      httpOnly: true
      secure: true

  coned_username: your-username
  coned_password: your-password
  coned: []

# Home Assistant configuration
home_assistant:
  enabled: true
  url: "http://yourdomain.local:5050"  # AppDaemon port, not HA port (8123)
  token: "your-long-lived-access-token"
  entity_id: "sensor.nyseg_energy_usage_direct"  # The sensor entity to populate
```

**Configuration Options:**
- `days_to_fetch`: Number of days of historical data to fetch from the API (default: 90 days / 3 months)

**Authentication:**
- `nyseg_username` / `nyseg_password`: Credentials for automatic login (recommended)
- `nyseg` cookies: Alternative cookie-based authentication (captured via `login` command)

**Home Assistant Configuration:**
- `enabled`: Set to `true` to enable Home Assistant publishing
- `url`: AppDaemon URL with port 5050 (not the Home Assistant port 8123)
- `token`: Long-lived access token from Home Assistant (Settings → Profile → Long-Lived Access Tokens)
- `entity_id`: The sensor entity ID to populate with historical data

## Project Structure

```
gridscraper/
├── cmd/gridscraper/       # CLI commands
│   ├── main.go           # Entry point
│   ├── root.go           # Root command & shared logic
│   ├── login.go          # Login command
│   ├── fetch.go          # Fetch command
│   ├── list.go           # List command
│   ├── publish.go        # Publish command (Home Assistant)
│   └── debug.go          # Debug command
├── internal/
│   ├── config/           # YAML config handling
│   │   └── config.go
│   ├── database/         # SQLite operations
│   │   └── db.go
│   ├── publisher/        # Home Assistant publishing
│   │   └── mqtt.go
│   └── scraper/          # Scraping logic
│       ├── browser.go    # Cookie management
│       └── nyseg.go      # NYSEG scraper
├── pkg/models/           # Data models
│   └── usage.go
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
go build -o gridscraper ./cmd/gridscraper
```

### Running Tests

```bash
go test ./...
```

## Supported Services

### NYSEG (New York State Electric & Gas)

- **Status**: Implemented
- **URL**: https://energymanager.nyseg.com/insights
- **Data**: Daily kWh usage from monthly view

### Con Edison

- **Status**: Planned (not yet implemented)
- **URL**: TBD

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
