package scraper

import (
	"context"
	"fmt"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/jgoulah/gridscraper/internal/config"
)

// ExtractCookies extracts all cookies from the current browser context
func ExtractCookies(ctx context.Context) ([]config.Cookie, error) {
	var cookies []*network.Cookie

	if err := chromedp.Run(ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			cookies, err = network.GetCookies().Do(ctx)
			return err
		}),
	); err != nil {
		return nil, fmt.Errorf("getting cookies: %w", err)
	}

	result := make([]config.Cookie, 0, len(cookies))
	for _, c := range cookies {
		result = append(result, config.Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Expires:  c.Expires,
			HTTPOnly: c.HTTPOnly,
			Secure:   c.Secure,
			SameSite: c.SameSite.String(),
		})
	}

	return result, nil
}

// SetCookies sets cookies in the browser context
func SetCookies(ctx context.Context, cookies []config.Cookie) error {
	if len(cookies) == 0 {
		return nil
	}

	for _, c := range cookies {
		expr := network.SetCookie(c.Name, c.Value).
			WithDomain(c.Domain).
			WithPath(c.Path).
			WithHTTPOnly(c.HTTPOnly).
			WithSecure(c.Secure)

		if err := chromedp.Run(ctx,
			chromedp.ActionFunc(func(ctx context.Context) error {
				return expr.Do(ctx)
			}),
		); err != nil {
			return fmt.Errorf("setting cookie %s: %w", c.Name, err)
		}
	}

	return nil
}
