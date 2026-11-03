package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

type Variant struct {
	Name     string   `json:"name"`
	Selected string   `json:"selected,omitempty"`
	Options  []string `json:"options"`
	Count    int      `json:"count"`
}

type Item struct {
	ID            string    `json:"id"`
	URL           string    `json:"url"`
	Title         string    `json:"title"`
	Price         float64   `json:"price"`
	Currency      string    `json:"currency"`
	Lowest30Days  float64   `json:"lowest_30_days,omitempty"`
	VATIncluded   bool      `json:"vat_included"`
	ImportCharges string    `json:"import_charges,omitempty"`
	ShipsFrom     string    `json:"ships_from,omitempty"`
	Delivery      string    `json:"delivery,omitempty"`
	Rating        string    `json:"rating,omitempty"`
	Reviews       string    `json:"reviews,omitempty"`
	Sold          string    `json:"sold,omitempty"`
	Store         string    `json:"store,omitempty"`
	StorePositive string    `json:"store_positive,omitempty"`
	Variants      []Variant `json:"variants,omitempty"`
	Bundle        bool      `json:"bundle_deals"`
}

var itemIDRe = regexp.MustCompile(`(?:/item/(?:[^/]*?/)?)?(\d{10,})`)

// ItemID accepts a bare id or any product URL.
func ItemID(arg string) (string, error) {
	if m := itemIDRe.FindStringSubmatch(arg); m != nil {
		return m[1], nil
	}
	return "", fmt.Errorf("%q is not an item id or an AliExpress item URL", arg)
}

const itemReadyJS = `(() => {
  const t = document.body.innerText;
  return /Sold By|Quantity|Cantidad|page you requested can not be found/i.test(t);
})()`

const itemJS = `(() => {
  const clean = (s) => (s || "").replace(/\s+/g, " ").trim();
  const variants = [];
  for (const p of document.querySelectorAll("[class*='sku-item--property--']")) {
    const t = p.querySelector("[class*='sku-item--title--']");
    const name = clean(t ? t.innerText : "").replace(/:.*$/, "");
    const selectedText = clean(t ? t.innerText : "").replace(/^[^:]*:\s*/, "");
    const options = [];
    for (const o of p.querySelectorAll("[class*='sku-item--skus--'] > *")) {
      const img = o.querySelector("img");
      const label = clean(o.innerText) || clean(o.getAttribute("title")) || clean(img && img.alt);
      if (label) options.push(label);
    }
    variants.push({name, selected: selectedText, options, count: options.length});
  }
  return JSON.stringify({text: document.body.innerText, variants});
})()`

// FetchItem loads one product page and reads it from the rendered text.
func FetchItem(ctx context.Context, id string) (*Item, error) {
	url := fmt.Sprintf("%s/item/%s.html", Host, id)
	if err := load(ctx, url, itemReadyJS, 30*time.Second); err != nil {
		return nil, err
	}
	var raw string
	if err := chromedp.Run(ctx, chromedp.Evaluate(itemJS, &raw)); err != nil {
		return nil, err
	}
	var page struct {
		Text     string    `json:"text"`
		Variants []Variant `json:"variants"`
	}
	if err := json.Unmarshal([]byte(raw), &page); err != nil {
		return nil, fmt.Errorf("parsing item page: %w", err)
	}
	if regexp.MustCompile(`(?i)page you requested can not be found`).MatchString(page.Text) {
		return nil, fmt.Errorf("item %s not found", id)
	}
	it, err := parseItem(page.Text, page.Variants)
	if err != nil {
		return nil, err
	}
	it.ID = id
	it.URL = url
	return it, nil
}

var (
	reviewsRe  = regexp.MustCompile(`(?m)^\s*(\d(?:\.\d)?)\s*$\s*^\s*([\d.,]+)\s+Reviews?\s*$`)
	soldRe     = regexp.MustCompile(`(?m)^\s*([\d.,]+[KM]?\+?)\s+sold\s*$`)
	lowestRe   = regexp.MustCompile(`30-day lowest price[^\n]*?(\d[\d.]*(?:,\d+)?\s?€)`)
	vatRe      = regexp.MustCompile(`(?m)^\s*Price includes VAT\b[^\n]*$`)
	shipsRe    = regexp.MustCompile(`(?i)ships? from[: ]+([A-Z][A-Za-z .'-]+?)\s*(?:\n|$)`)
	positiveRe = regexp.MustCompile(`([\d.]+)%\s+Positive Feedback`)
	deliveryRe = regexp.MustCompile(`(?m)^\s*(Delivery:[^\n]+|Fastest delivery in[^\n]+)$`)
)

func parseItem(text string, variants []Variant) (*Item, error) {
	text = strings.ReplaceAll(text, "\u00a0", " ")
	it := &Item{Currency: "EUR"}
	for _, v := range variants {
		seen := map[string]bool{}
		var uniq []string
		for _, o := range v.Options {
			if !seen[o] {
				seen[o] = true
				uniq = append(uniq, o)
			}
		}
		v.Options = uniq
		it.Variants = append(it.Variants, v)
	}

	head := text
	if i := strings.Index(text, "Customer Reviews"); i > 0 {
		head = text[:i]
	}
	service := ""
	if i := strings.Index(text, "Sold By"); i >= 0 {
		service = text[i:]
		if j := strings.Index(service, "Quantity"); j > 0 {
			service = service[:j]
		}
	}

	var priceFrom int
	if m := reviewsRe.FindStringSubmatchIndex(head); m != nil {
		it.Rating = head[m[2]:m[3]]
		it.Reviews = head[m[4]:m[5]]
		before := strings.Split(strings.TrimRight(head[:m[0]], " \n"), "\n")
		it.Title = clean(before[len(before)-1])
		priceFrom = m[1]
	} else if loc := priceRe.FindStringIndex(head); loc != nil {
		before := strings.Split(strings.TrimRight(head[:loc[0]], " \n"), "\n")
		it.Title = clean(before[len(before)-1])
		priceFrom = loc[0]
	}
	if it.Title == "" {
		return nil, fmt.Errorf("could not find the item title; the page layout may have changed (try --show)")
	}
	if m := soldRe.FindStringSubmatch(head[priceFrom:]); m != nil {
		it.Sold = m[1]
	}
	it.Price = money(head[priceFrom:])
	if it.Price == 0 {
		return nil, fmt.Errorf("found %q but no price; the item may be unavailable in this market", it.Title)
	}
	if m := lowestRe.FindStringSubmatch(head); m != nil {
		it.Lowest30Days = money(m[1])
	}
	if line := vatRe.FindString(head); line != "" {
		it.VATIncluded = true
		switch {
		case strings.Contains(line, "No import charges"):
			it.ImportCharges = "none"
		case strings.Contains(line, "Import charges will apply"):
			it.ImportCharges = "apply"
		}
	}

	if m := regexp.MustCompile(`Sold By\s*\n\s*([^\n]+)`).FindStringSubmatch(text); m != nil {
		it.Store = clean(m[1])
	}
	if m := positiveRe.FindStringSubmatch(text); m != nil {
		it.StorePositive = m[1] + "%"
	}
	if m := shipsRe.FindStringSubmatch(service); m != nil {
		it.ShipsFrom = clean(m[1])
	} else if m := regexp.MustCompile(`Ships From:\s*([A-Za-z .'-]+)`).FindStringSubmatch(text); m != nil {
		it.ShipsFrom = clean(m[1])
	}
	if m := deliveryRe.FindStringSubmatch(service); m != nil {
		it.Delivery = clean(m[1])
	}
	it.Bundle = bundleRe.MatchString(head) || bundleRe.MatchString(service)
	return it, nil
}
