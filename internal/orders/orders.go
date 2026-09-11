// Package orders reads the signed-in account's order list off the
// AliExpress web app.
package orders

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

const (
	ListURL  = "https://www.aliexpress.com/p/order/index.html"
	LoginURL = "https://login.aliexpress.com/"
)

type Item struct {
	Title   string `json:"title"`
	URL     string `json:"url,omitempty"`
	Image   string `json:"image,omitempty"`
	Variant string `json:"variant,omitempty"`
	Qty     string `json:"qty,omitempty"`
	Price   string `json:"price,omitempty"`
}

type Order struct {
	ID     string `json:"id"`
	Date   string `json:"date,omitempty"`
	Status string `json:"status,omitempty"`
	Total  string `json:"total,omitempty"`
	Store  string `json:"store,omitempty"`
	URL    string `json:"url,omitempty"`
	Items  []Item `json:"items"`
}

// LoggedIn reports whether the restored session is usable, by asking
// for the order list and seeing where we land.
func LoggedIn(ctx context.Context) (bool, error) {
	var current string
	err := chromedp.Run(ctx,
		chromedp.Navigate(ListURL),
		chromedp.Sleep(4*time.Second),
		chromedp.Location(&current),
	)
	if err != nil {
		return false, err
	}
	return !isLoginPage(current), nil
}

func isLoginPage(u string) bool {
	for _, s := range []string{"login.aliexpress", "ug-login-page", "/login.htm"} {
		if strings.Contains(u, s) {
			return true
		}
	}
	return false
}

// List loads the order list and keeps pressing the page's own "View
// orders" button until it has enough, or until the list stops growing.
func List(ctx context.Context, limit int) ([]Order, error) {
	var current string
	if err := chromedp.Run(ctx,
		chromedp.Navigate(ListURL),
		chromedp.Sleep(5*time.Second),
		chromedp.Location(&current),
	); err != nil {
		return nil, err
	}
	if isLoginPage(current) {
		return nil, fmt.Errorf("not signed in; run `ali login` first")
	}

	const maxPages = 60
	seen := -1
	for pass := 0; pass < maxPages; pass++ {
		got, err := extract(ctx)
		if err != nil {
			return nil, err
		}
		if limit > 0 && len(got) >= limit {
			return got[:limit], nil
		}
		if len(got) == seen {
			return got, nil
		}
		seen = len(got)

		var more bool
		if err := chromedp.Run(ctx, chromedp.Evaluate(clickMoreJS, &more)); err != nil {
			return nil, err
		}
		if !more {
			return got, nil
		}
		if err := chromedp.Run(ctx, chromedp.Sleep(2500*time.Millisecond)); err != nil {
			return nil, err
		}
	}
	return extract(ctx)
}

func extract(ctx context.Context) ([]Order, error) {
	var raw string
	if err := chromedp.Run(ctx, chromedp.Evaluate(extractJS, &raw)); err != nil {
		return nil, err
	}
	var got []Order
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		return nil, fmt.Errorf("parsing extracted orders: %w", err)
	}
	return got, nil
}

const clickMoreJS = `(() => {
  const btn = document.querySelector(".order-more button");
  if (!btn || btn.disabled || btn.offsetParent === null) return false;
  btn.click();
  return true;
})()`

// DumpHTML returns the rendered order page, for fixing selectors when
// AliExpress changes its markup.
func DumpHTML(ctx context.Context) (string, error) {
	var html string
	err := chromedp.Run(ctx,
		chromedp.Navigate(ListURL),
		chromedp.Sleep(6*time.Second),
		chromedp.OuterHTML("html", &html),
	)
	return html, err
}

// WaitForLogin opens the order list once and then watches the address
// bar without touching it, so a sign-in in progress is never reloaded
// out from under the person doing it. It returns when the session is
// live, when done fires, or when ctx is cancelled.
func WaitForLogin(ctx context.Context, done <-chan struct{}) error {
	if err := chromedp.Run(ctx, chromedp.Navigate(ListURL)); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			ok, err := LoggedIn(ctx)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("that profile still looks signed out")
			}
			return nil
		default:
		}

		var current string
		if err := chromedp.Run(ctx, chromedp.Location(&current)); err != nil {
			return err
		}
		if current != "" && !isLoginPage(current) {
			ok, err := LoggedIn(ctx)
			if err != nil {
				return err
			}
			if ok {
				return nil
			}
			if err := chromedp.Run(ctx, chromedp.Navigate(ListURL)); err != nil {
				return err
			}
		}
		time.Sleep(3 * time.Second)
	}
}

const detailURL = "https://www.aliexpress.com/p/order/detail.html?orderId="

// Expand fills in item titles for orders whose list entry showed only
// thumbnails, which is how AliExpress renders an order of several
// items. Each one costs a page load, so callers ask for it explicitly.
func Expand(ctx context.Context, in []Order) ([]Order, error) {
	out := make([]Order, len(in))
	copy(out, in)
	for idx, o := range out {
		if hasTitles(o) {
			continue
		}
		var raw string
		if err := chromedp.Run(ctx,
			chromedp.Navigate(detailURL+o.ID),
			chromedp.Sleep(4*time.Second),
			chromedp.Evaluate(detailJS, &raw),
		); err != nil {
			return out, err
		}
		var items []Item
		if err := json.Unmarshal([]byte(raw), &items); err != nil {
			return out, fmt.Errorf("parsing order %s: %w", o.ID, err)
		}
		if len(items) > 0 {
			out[idx].Items = items
		}
	}
	return out, nil
}

func hasTitles(o Order) bool {
	for _, it := range o.Items {
		if it.Title != "" {
			return true
		}
	}
	return false
}
