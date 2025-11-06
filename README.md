# GridScraper

A Go-based CLI tool to scrape electrical usage data from utility websites (NYSEG and Con Edison). Uses browser automation to extract daily kWh data and stores it in a local SQLite database.

## Features

- **Cookie-based authentication**: Login once, scrape multiple times
- **Browser automation**: Uses chromedp for JavaScript-rendered pages
- **Duplicate prevention**: Won't re-scrape existing data
- **SQLite storage**: Local database for tracking usage over time
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

### 1. Login and Save Cookies

First, login to the service to save authentication cookies:

```bash
# Login to NYSEG
gridscraper login nyseg
```

This will:
- Open a browser window
- Navigate to the NYSEG login page
- Wait for you to manually log in
- Extract and save cookies to `./config.yaml`

Press Enter after successfully logging in to save the cookies.

### 2. Fetch Usage Data

Once authenticated, fetch your usage data:

```bash
# Fetch NYSEG data
gridscraper fetch nyseg
```

This will:
- Use saved cookies to access the insights page
- Click the "month" button to view monthly data
- Extract daily kWh values from the bar chart
- Store data in `./data.db`
- Skip any dates that already exist (duplicate prevention)

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

### 4. Debug Scraping Issues

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
cookies:
  nyseg:
    - name: session_id
      value: abc123...
      domain: energymanager.nyseg.com
      path: /
      httpOnly: true
      secure: true
  coned: []
```

## Project Structure

```
gridscraper/
├── cmd/gridscraper/       # CLI commands
│   ├── main.go           # Entry point
│   ├── root.go           # Root command & shared logic
│   ├── login.go          # Login command
│   ├── fetch.go          # Fetch command
│   ├── list.go           # List command
│   └── debug.go          # Debug command
├── internal/
│   ├── config/           # YAML config handling
│   │   └── config.go
│   ├── database/         # SQLite operations
│   │   └── db.go
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
