package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/jgoulah/gridscraper/internal/config"
	"github.com/jgoulah/gridscraper/internal/scraper"
	"github.com/spf13/cobra"
)

var inspectVisible bool

var inspectCmd = &cobra.Command{
	Use:   "inspect [service]",
	Short: "Inspect tooltip by programmatically triggering hover",
	Long:  `Programmatically hovers over chart bars and captures tooltip HTML.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runInspect,
}

func init() {
	inspectCmd.Flags().BoolVar(&inspectVisible, "visible", false, "Show browser window")
	rootCmd.AddCommand(inspectCmd)
}

func runInspect(cmd *cobra.Command, args []string) error {
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

	// Get cookies
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
		chromedp.Flag("headless", !inspectVisible),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.UserAgent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()

	browserCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	browserCtx, cancel = context.WithTimeout(browserCtx, 3*time.Minute)
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

	// Click month button
	fmt.Println("Clicking month button...")
	if err := chromedp.Run(browserCtx,
		chromedp.Click(`div.engage-insights-explore__button`, chromedp.ByQuery),
		chromedp.Sleep(3*time.Second),
	); err != nil {
		return fmt.Errorf("clicking month button: %w", err)
	}

	// Capture tooltip by triggering events and watching for visibility changes
	fmt.Println("Capturing tooltip data...")
	var tooltipInfo interface{}
	if err := chromedp.Run(browserCtx,
		chromedp.Evaluate(`
			(async function() {
				const bars = document.querySelectorAll('rect[id="engage-chart-bar"]');
				if (bars.length === 0) return {error: "No bars found"};

				// Take a snapshot of element visibility BEFORE hover
				const allElements = document.querySelectorAll('*');
				const beforeState = new Map();

				for (let el of allElements) {
					const style = window.getComputedStyle(el);
					const isVisible = style.display !== 'none' &&
					                 style.visibility !== 'hidden' &&
					                 parseFloat(style.opacity) > 0;
					beforeState.set(el, isVisible);
				}

				// Get the first bar and trigger hover
				const bar = bars[0];
				const rect = bar.getBoundingClientRect();
				const centerX = rect.left + rect.width / 2;
				const centerY = rect.top + rect.height / 2;

				// Trigger multiple events
				const events = ['mouseover', 'mouseenter', 'mousemove'];
				for (let eventType of events) {
					const event = new MouseEvent(eventType, {
						view: window,
						bubbles: true,
						cancelable: true,
						clientX: centerX,
						clientY: centerY
					});
					bar.dispatchEvent(event);
				}

				// Wait for tooltip to appear
				await new Promise(resolve => setTimeout(resolve, 1000));

				// Find elements that became visible
				const changedElements = [];
				const newElements = [];

				for (let el of document.querySelectorAll('*')) {
					const style = window.getComputedStyle(el);
					const isVisibleNow = style.display !== 'none' &&
					                    style.visibility !== 'hidden' &&
					                    parseFloat(style.opacity) > 0;

					const wasVisibleBefore = beforeState.get(el);

					// Check if element is newly visible
					if (isVisibleNow && !wasVisibleBefore) {
						const text = el.textContent.trim();
						if (text) {
							changedElements.push({
								tag: el.tagName,
								className: el.className,
								id: el.id,
								textContent: text.substring(0, 200),
								outerHTML: el.outerHTML.substring(0, 800),
								innerHTML: el.innerHTML.substring(0, 800)
							});
						}
					}

					// Also check for elements we didn't see before (dynamic creation)
					if (isVisibleNow && !beforeState.has(el)) {
						const text = el.textContent.trim();
						if (text) {
							newElements.push({
								tag: el.tagName,
								className: el.className,
								id: el.id,
								textContent: text.substring(0, 200),
								outerHTML: el.outerHTML.substring(0, 800)
							});
						}
					}
				}

				return {
					barCount: bars.length,
					visibilityChanged: changedElements.length,
					changedElements: changedElements,
					newElementsCreated: newElements.length,
					newElements: newElements
				};
			})()
		`, &tooltipInfo),
	); err != nil {
		return fmt.Errorf("capturing tooltip: %w", err)
	}

	// Pretty print the result
	fmt.Println("\n=== TOOLTIP INSPECTION RESULTS ===")
	jsonBytes, _ := json.MarshalIndent(tooltipInfo, "", "  ")
	fmt.Println(string(jsonBytes))
	fmt.Println("==================================\n")

	return nil
}
