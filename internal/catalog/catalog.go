// Package catalog reads public product pages and search results off the
// AliExpress storefront, using whatever session the browser restored.
package catalog

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

const Host = "https://es.aliexpress.com"

var blocked = regexp.MustCompile(`(?i)captcha|punish|_____tmd_____|login\.aliexpress|/login\.htm`)

const blockedTextJS = `/slide to verify|unusual traffic|verify you are human|deslice para verificar/i.test(document.body.innerText)`

// load navigates and waits until ready() reports true, polling at a
// gentle pace. It stops with an error on a captcha or login wall rather
// than trying to get past it.
func load(ctx context.Context, url, readyJS string, wait time.Duration) error {
	if err := chromedp.Run(ctx, chromedp.Navigate(url)); err != nil {
		return err
	}
	deadline := time.Now().Add(wait)
	for {
		if err := chromedp.Run(ctx, chromedp.Sleep(1500*time.Millisecond)); err != nil {
			return err
		}
		var loc string
		var walled, ready bool
		if err := chromedp.Run(ctx,
			chromedp.Location(&loc),
			chromedp.Evaluate(blockedTextJS, &walled),
			chromedp.Evaluate(readyJS, &ready),
		); err != nil {
			return err
		}
		if walled || blocked.MatchString(loc) {
			return fmt.Errorf("AliExpress is asking for a captcha or login; stopping (open the site yourself, or run `ali status`)")
		}
		if ready {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("page did not finish rendering in %s", wait)
		}
	}
}

func clean(s string) string {
	return strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(s, " "))
}

var priceRe = regexp.MustCompile(`(\d[\d.]*(?:,\d+)?)\s?€`)

// money turns a storefront price such as "1.234,56€" into 1234.56.
func money(s string) float64 {
	m := priceRe.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	n := strings.ReplaceAll(strings.ReplaceAll(m[1], ".", ""), ",", ".")
	var f float64
	fmt.Sscanf(n, "%f", &f)
	return f
}

var bundleRe = regexp.MustCompile(`(?i)bundle deals?|buy \d+\+[^\n]*duty covered|duty covered`)
