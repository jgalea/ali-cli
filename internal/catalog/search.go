package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

type Result struct {
	ID        string  `json:"id"`
	URL       string  `json:"url"`
	Title     string  `json:"title"`
	Price     float64 `json:"price"`
	Currency  string  `json:"currency"`
	Rating    string  `json:"rating,omitempty"`
	Sold      string  `json:"sold,omitempty"`
	ShipsFrom string  `json:"ships_from,omitempty"`
	Badge     string  `json:"badge,omitempty"`
	Bundle    bool    `json:"bundle_deals"`
}

// SearchURL builds the wholesale URL. shipsFrom is "EU", a two-letter
// country code, or empty. The storefront's own filter does the work:
// shpf_co for a country, the shipfrom switch for the EU.
func SearchURL(query, shipsFrom string) (string, error) {
	slug := strings.Join(strings.Fields(strings.ToLower(query)), "-")
	if slug == "" {
		return "", fmt.Errorf("empty search query")
	}
	u := fmt.Sprintf("%s/w/wholesale-%s.html?SearchText=%s", Host, url.PathEscape(slug), url.QueryEscape(query))
	switch s := strings.ToUpper(strings.TrimSpace(shipsFrom)); {
	case s == "":
	case s == "EU":
		u += "&selectedSwitches=" + url.QueryEscape("filterCode:shipfrom")
	case regexp.MustCompile(`^[A-Z]{2}$`).MatchString(s):
		u += "&shpf_co=" + s
	default:
		return "", fmt.Errorf("--ships-from takes EU or a two-letter country code, got %q", shipsFrom)
	}
	return u, nil
}

const cardsReadyJS = `document.querySelectorAll("a.search-card-item").length > 0 || /did not match any|no results|No se encontr/i.test(document.body.innerText)`

const cardsJS = `(() => {
  const out = [];
  for (const c of document.querySelectorAll("a.search-card-item")) {
    out.push({
      href: c.getAttribute("href") || "",
      text: c.innerText,
      alts: [...c.querySelectorAll("img")].map((i) => i.alt || ""),
    });
  }
  return JSON.stringify(out);
})()`

const scrollJS = `window.scrollBy(0, 1400)`

// Search loads one results page and scrolls it until limit cards have
// rendered or the page stops growing.
func Search(ctx context.Context, query, shipsFrom string, limit int) ([]Result, error) {
	u, err := SearchURL(query, shipsFrom)
	if err != nil {
		return nil, err
	}
	if err := load(ctx, u, cardsReadyJS, 30*time.Second); err != nil {
		return nil, err
	}

	var raw string
	seen := -1
	for pass := 0; pass < 8; pass++ {
		if err := chromedp.Run(ctx, chromedp.Evaluate(cardsJS, &raw)); err != nil {
			return nil, err
		}
		var n []json.RawMessage
		json.Unmarshal([]byte(raw), &n)
		if len(n) >= limit || len(n) == seen {
			break
		}
		seen = len(n)
		if err := chromedp.Run(ctx, chromedp.Evaluate(scrollJS, nil), chromedp.Sleep(1500*time.Millisecond)); err != nil {
			return nil, err
		}
	}

	var cards []struct {
		Href string   `json:"href"`
		Text string   `json:"text"`
		Alts []string `json:"alts"`
	}
	if err := json.Unmarshal([]byte(raw), &cards); err != nil {
		return nil, fmt.Errorf("parsing results: %w", err)
	}
	var out []Result
	for _, c := range cards {
		r, ok := parseCard(c.Href, c.Text, c.Alts)
		if !ok {
			continue
		}
		if shipsFrom != "" {
			r.ShipsFrom = strings.ToUpper(shipsFrom)
		}
		out = append(out, r)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	if len(out) > 0 && !anyMatch(out, query) {
		return nil, fmt.Errorf("no results for %q (AliExpress filled the page with unrelated items)", query)
	}
	return out, nil
}

// anyMatch reports whether at least one title shares a word with the
// query. A query with no real hits gets substitute items, not an empty page.
func anyMatch(rs []Result, query string) bool {
	var words []string
	for _, w := range strings.Fields(strings.ToLower(query)) {
		if len(w) >= 3 {
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		return true
	}
	for _, r := range rs {
		t := strings.ToLower(r.Title)
		for _, w := range words {
			if strings.Contains(t, w) {
				return true
			}
		}
	}
	return false
}

var (
	cardIDRe     = regexp.MustCompile(`/item/(\d+)`)
	cardRatingRe = regexp.MustCompile(`^\d(?:\.\d)?$`)
	cardSoldRe   = regexp.MustCompile(`^([\d.,]+[KM]?\+?) sold$`)
)

func parseCard(href, text string, alts []string) (Result, bool) {
	m := cardIDRe.FindStringSubmatch(href)
	if m == nil {
		return Result{}, false
	}
	text = strings.ReplaceAll(text, "\u00a0", " ")
	r := Result{ID: m[1], URL: fmt.Sprintf("%s/item/%s.html", Host, m[1]), Currency: "EUR"}
	for _, line := range strings.Split(text, "\n") {
		line = clean(line)
		switch {
		case line == "":
		case line == "Ad":
		case r.Title == "":
			r.Title = line
		case r.Price == 0 && priceRe.MatchString(line):
			r.Price = money(line)
		case r.Rating == "" && cardRatingRe.MatchString(line):
			r.Rating = line
		case r.Sold == "" && cardSoldRe.MatchString(line):
			r.Sold = cardSoldRe.FindStringSubmatch(line)[1]
		case strings.EqualFold(line, "Free shipping"), strings.EqualFold(line, "Choice"):
			r.Badge = line
		}
	}
	for _, a := range alts {
		if strings.EqualFold(clean(a), "Choice") && r.Badge == "" {
			r.Badge = "Choice"
		}
	}
	r.Bundle = bundleRe.MatchString(text) || bundleRe.MatchString(strings.Join(alts, "\n"))
	return r, r.Title != "" && r.Price > 0
}
