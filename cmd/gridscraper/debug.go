package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/jgoulah/gridscraper/internal/config"
	"github.com/jgoulah/gridscraper/internal/scraper"
	"github.com/spf13/cobra"
)

var (
	debugVisible bool
	debugOutput  string
)

var debugCmd = &cobra.Command{
	Use:   "debug [service]",
	Short: "Debug scraper by opening visible browser or saving HTML",
	Long: `Opens a visible browser or saves HTML to help debug scraper issues.

Available services: nyseg, coned

Flags:
  --visible    Open visible browser and pause for inspection
  --output     Save HTML to file instead of displaying`,
	Args: cobra.ExactArgs(1),
	RunE: runDebug,
}

func init() {
	debugCmd.Flags().BoolVar(&debugVisible, "visible", false, "Open visible browser and pause")
	debugCmd.Flags().StringVar(&debugOutput, "output", "", "Save HTML to this file")
	rootCmd.AddCommand(debugCmd)
}

func runDebug(cmd *cobra.Command, args []string) error {
	service := args[0]

	var loginURL string
	switch service {
	case "nyseg":
		loginURL = "https://energymanager.nyseg.com/insights"
	case "coned":
		return fmt.Errorf("Con Edison support not yet implemented")
	default:
		return fmt.Errorf("unknown service: %s (available: nyseg, coned)", service)
	}

	// Load config
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Get cookies for service
	var cookies []config.Cookie
	switch service {
	case "nyseg":
		cookies = cfg.Cookies.NYSEG
	case "coned":
		cookies = cfg.Cookies.ConEd
	}

	if len(cookies) == 0 {
		return fmt.Errorf("no cookies found for %s. Run 'gridscraper login %s' first", service, service)
	}

	// Setup browser
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", !debugVisible),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()

	browserCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	browserCtx, cancel = context.WithTimeout(browserCtx, 5*time.Minute)
	defer cancel()

	// Set cookies and navigate
	if err := scraper.SetCookies(browserCtx, cookies); err != nil {
		return fmt.Errorf("setting cookies: %w", err)
	}

	fmt.Printf("Navigating to %s...\n", loginURL)

	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(loginURL),
		chromedp.WaitVisible(`div.engage-insights-explore`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("navigating: %w", err)
	}

	// Try to find and click month button
	fmt.Println("Looking for month button...")

	// First, let's see what buttons exist
	var buttonCount int
	if err := chromedp.Run(browserCtx,
		chromedp.Evaluate(`document.querySelectorAll('div.engage-insights-explore__button').length`, &buttonCount),
	); err != nil {
		fmt.Printf("Warning: Could not count buttons: %v\n", err)
	} else {
		fmt.Printf("Found %d button(s) with class 'engage-insights-explore__button'\n", buttonCount)
	}

	// Try to click the button
	fmt.Println("Attempting to click month button...")
	err = chromedp.Run(browserCtx,
		chromedp.Click(`div.engage-insights-explore__button`, chromedp.ByQuery),
		chromedp.Sleep(3*time.Second),
	)
	if err != nil {
		fmt.Printf("Warning: Could not click button: %v\n", err)
		fmt.Println("Continuing anyway to inspect page...")
	} else {
		fmt.Println("✓ Button clicked successfully")
	}

	// First, let's inspect what data is already in the DOM
	fmt.Println("\nInspecting chart structure...")
	var chartStructure string
	if err := chromedp.Run(browserCtx,
		chromedp.Evaluate(`
			(function() {
				const bars = document.querySelectorAll('rect[id="engage-chart-bar"]');

				const info = {
					barCount: bars.length,
					sampleBar: null,
					parentElements: [],
					siblingElements: []
				};

				if (bars.length > 0) {
					const bar = bars[0];

					// Get bar info
					const barAttrs = {};
					for (let attr of bar.attributes) {
						barAttrs[attr.name] = attr.value;
					}
					info.sampleBar = {
						tag: bar.tagName,
						attributes: barAttrs
					};

					// Check parent elements for data
					let parent = bar.parentElement;
					let depth = 0;
					while (parent && depth < 3) {
						const parentInfo = {
							depth: depth,
							tag: parent.tagName,
							class: parent.className,
							id: parent.id,
							childCount: parent.children.length
						};
						info.parentElements.push(parentInfo);
						parent = parent.parentElement;
						depth++;
					}

					// Check siblings
					const parentNode = bar.parentElement;
					if (parentNode) {
						Array.from(parentNode.children).forEach(child => {
							if (child !== bar && (child.textContent || child.tagName === 'text')) {
								info.siblingElements.push({
									tag: child.tagName,
									class: child.className,
									text: child.textContent.trim().substring(0, 100)
								});
							}
						});
					}
				}

				return JSON.stringify(info, null, 2);
			})()
		`, &chartStructure),
	); err != nil {
		fmt.Printf("Warning: Could not inspect structure: %v\n", err)
	} else {
		fmt.Printf("Chart structure:\n%s\n\n", chartStructure)
	}

	// Search for all text elements and data in the chart area
	fmt.Println("Searching for text/data elements in chart...")
	var dataElements string
	if err := chromedp.Run(browserCtx,
		chromedp.Evaluate(`
			(function() {
				// Look for all text elements, labels, and data attributes
				const container = document.querySelector('div.engage-insights-explore');
				if (!container) return JSON.stringify({error: "Container not found"});

				const results = {
					textElements: [],
					allKwhReferences: []
				};

				// Find all text and tspan elements (common in SVG charts)
				const textEls = container.querySelectorAll('text, tspan');
				textEls.forEach((el, idx) => {
					if (idx < 20) { // Limit output
						results.textElements.push({
							tag: el.tagName,
							class: el.className.baseVal || el.className,
							text: el.textContent.trim()
						});
					}
				});

				// Search entire page for elements with kWh
				document.querySelectorAll('*').forEach(el => {
					const text = el.textContent;
					if (text.includes('kWh') && text.length < 200) {
						results.allKwhReferences.push({
							tag: el.tagName,
							class: el.className.baseVal || el.className,
							text: text.trim().substring(0, 150)
						});
					}
				});

				return JSON.stringify(results, null, 2);
			})()
		`, &dataElements),
	); err != nil {
		fmt.Printf("Warning: Could not search for data: %v\n", err)
	} else {
		fmt.Printf("Data elements found:\n%s\n\n", dataElements)
	}

	fmt.Println("IMPORTANT: With the browser open, manually hover over a bar and:")
	fmt.Println("1. Right-click -> Inspect on the tooltip popup")
	fmt.Println("2. Note the HTML structure and selectors")
	fmt.Println("3. Share the structure so we can update the scraper\n")

	// Extract HTML
	var html string
	if err := chromedp.Run(browserCtx,
		chromedp.OuterHTML(`div.engage-insights-explore`, &html, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("extracting HTML: %w", err)
	}

	// Handle output
	if debugOutput != "" {
		if err := os.WriteFile(debugOutput, []byte(html), 0644); err != nil {
			return fmt.Errorf("writing output file: %w", err)
		}
		fmt.Printf("✓ HTML saved to %s\n", debugOutput)
	} else if !debugVisible {
		fmt.Println(html)
	}

	if debugVisible {
		fmt.Println("\nBrowser is open. Inspect the page, then press Enter to close...")
		fmt.Scanln()
	}

	return nil
}
