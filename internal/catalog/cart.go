package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

const (
	cartURL   = Host + "/p/shoppingcart/index.html"
	bundleURL = "https://www.aliexpress.com/ssr/300000512/BundleDealsDutyCovered?disableNav=YES&pha_manifest=ssr&_immersiveMode=true&businessCode=guide"
)

type Line struct {
	ID            string   `json:"id,omitempty"`
	Title         string   `json:"title"`
	Variant       string   `json:"variant,omitempty"`
	Store         string   `json:"store,omitempty"`
	ShippedBy     string   `json:"shipped_by,omitempty"`
	Quantity      int      `json:"quantity"`
	UnitPrice     float64  `json:"unit_price"`
	Currency      string   `json:"currency"`
	PriceNotes    []string `json:"price_notes,omitempty"`
	VATIncluded   bool     `json:"vat_included"`
	ImportCharges string   `json:"import_charges,omitempty"`
	Shipping      string   `json:"shipping,omitempty"`
	StockWarning  string   `json:"stock_warning,omitempty"`
	Notice        string   `json:"notice,omitempty"`
	Available     bool     `json:"available"`
	Selected      bool     `json:"selected"`
}

type Summary struct {
	Subtotal       float64 `json:"subtotal"`
	Shipping       string  `json:"shipping"`
	ShippingFree   bool    `json:"shipping_free"`
	EstimatedTotal float64 `json:"estimated_total"`
	Selected       int     `json:"selected"`
}

type Bundle struct {
	Reachable bool   `json:"reachable"`
	Note      string `json:"note,omitempty"`
	Needed    int    `json:"needed"`
	Status    string `json:"status,omitempty"`
	Items     []Line `json:"items,omitempty"`
}

type Cart struct {
	Declared int      `json:"declared_lines"`
	Lines    []Line   `json:"lines"`
	Summary  Summary  `json:"summary"`
	Bundle   Bundle   `json:"bundle_basket"`
	Warnings []string `json:"warnings,omitempty"`
}

type rawLine struct {
	Title     string   `json:"title"`
	Href      string   `json:"href"`
	Variant   string   `json:"variant"`
	Store     string   `json:"store"`
	ShippedBy string   `json:"shipped_by"`
	Price     string   `json:"price"`
	Notes     []string `json:"notes"`
	Tax       string   `json:"tax"`
	Shipping  string   `json:"shipping"`
	Stock     string   `json:"stock"`
	Notice    string   `json:"notice"`
	Qty       string   `json:"qty"`
	Checked   bool     `json:"checked"`
	Available bool     `json:"available"`
}

// The storefront renders in Spanish or English depending on the session,
// sometimes switching between runs. These are the only labels read.
var (
	cartVATRe     = regexp.MustCompile(`(?i)price includes vat|precio con iva incluido`)
	chargesNoneRe = regexp.MustCompile(`(?i)no import charges|sin gastos de importaci[oó]n`)
	chargesApply  = regexp.MustCompile(`(?i)import charges will apply|se aplicar[aá]n cargos de importaci[oó]n`)
	freeRe        = regexp.MustCompile(`(?i)^(free|gratis)\b`)
	neededRe      = regexp.MustCompile(`(?i)\b(?:add|a[ñn]ade|a[ñn]adir|agrega)\s+(\d+)`)
	countRe       = regexp.MustCompile(`\((\d+)\)`)
	itemHrefRe    = regexp.MustCompile(`/item/(\d+)\.html`)
	summaryLabels = map[string]string{
		"subtotal":         "subtotal",
		"shipping":         "shipping",
		"envío":            "shipping",
		"envio":            "shipping",
		"estimated total":  "total",
		"estimación total": "total",
		"estimacion total": "total",
	}
)

func parseLine(r rawLine) (Line, error) {
	l := Line{
		Title:        clean(r.Title),
		Variant:      clean(r.Variant),
		Store:        clean(r.Store),
		ShippedBy:    clean(r.ShippedBy),
		Currency:     "EUR",
		UnitPrice:    money(strings.ReplaceAll(r.Price, " ", " ")),
		Shipping:     clean(r.Shipping),
		StockWarning: clean(r.Stock),
		Notice:       clean(r.Notice),
		Available:    r.Available,
		Selected:     r.Checked,
		Quantity:     1,
	}
	if l.Title == "" {
		return l, fmt.Errorf("cart line without a title; the page layout may have changed")
	}
	if m := itemHrefRe.FindStringSubmatch(r.Href); m != nil {
		l.ID = m[1]
	}
	if n, err := strconv.Atoi(strings.TrimSpace(r.Qty)); err == nil && n > 0 {
		l.Quantity = n
	}
	for _, n := range r.Notes {
		if n = clean(n); n != "" {
			l.PriceNotes = append(l.PriceNotes, n)
		}
	}
	tax := clean(r.Tax)
	if tax != "" {
		l.VATIncluded = cartVATRe.MatchString(tax)
		switch {
		case chargesNoneRe.MatchString(tax):
			l.ImportCharges = "none"
		case chargesApply.MatchString(tax):
			l.ImportCharges = "apply"
		default:
			return l, fmt.Errorf("unrecognised tax label %q on %q; add it to the label map in cart.go", tax, truncateRunes(l.Title, 40))
		}
	}
	if l.Available && l.UnitPrice == 0 {
		return l, fmt.Errorf("no price found for %q; the page layout may have changed", truncateRunes(l.Title, 40))
	}
	return l, nil
}

func parseLines(raw []rawLine) ([]Line, error) {
	out := make([]Line, 0, len(raw))
	for _, r := range raw {
		l, err := parseLine(r)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, nil
}

// parseSummary reads the label/value pairs of the order summary, which
// come as alternating lines. checkout is the text of the checkout button.
func parseSummary(text, checkout string) (Summary, error) {
	var s Summary
	lines := strings.Split(strings.ReplaceAll(text, " ", " "), "\n")
	found := map[string]bool{}
	for i := 0; i+1 < len(lines); i++ {
		kind, ok := summaryLabels[strings.ToLower(clean(lines[i]))]
		if !ok || found[kind] {
			continue
		}
		val := clean(lines[i+1])
		switch kind {
		case "subtotal":
			s.Subtotal = money(val)
		case "total":
			s.EstimatedTotal = money(val)
		case "shipping":
			s.Shipping = val
			s.ShippingFree = freeRe.MatchString(val)
		}
		found[kind] = true
	}
	for _, k := range []string{"subtotal", "shipping", "total"} {
		if !found[k] {
			return s, fmt.Errorf("order summary has no %q label; the page text may be in a language not in the label map", k)
		}
	}
	if m := countRe.FindStringSubmatch(checkout); m != nil {
		s.Selected, _ = strconv.Atoi(m[1])
	}
	return s, nil
}

func parseBundleNeeded(reminder string) int {
	if m := neededRe.FindStringSubmatch(reminder); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

const cartReadyJS = `!!document.querySelector('.cart-product, .cart-summary')`

const cartCountJS = `(() => {
  const h = document.querySelector('.cart-header-title');
  const m = h && h.innerText.match(/\((\d+)\)/);
  return JSON.stringify({lines: document.querySelectorAll('.cart-main .cart-product').length, declared: m ? +m[1] : 0});
})()`

const linesJS = `((root) => {
  const clean = (s) => (s || "").replace(/\s+/g, " ").trim();
  const out = [];
  for (const el of document.querySelectorAll(root + " .cart-product")) {
    const q = (s) => el.querySelector(s);
    const txt = (s) => { const e = q(s); return e ? clean(e.innerText || e.textContent) : ""; };
    const a = q("a[href*='/item/']");
    const cb = q("input[type=checkbox]");
    const sku = q(".skuStr");
    const group = el.closest(".group-container");
    const gt = group && group.querySelector(".group-title-content");
    const title = q(".cart-product-name-title");
    out.push({
      title: title ? clean(title.getAttribute("title") || title.innerText) : "",
      href: a ? a.getAttribute("href") : "",
      variant: sku ? clean(sku.getAttribute("title") || sku.textContent) : "",
      store: txt(".cart-product-shop-name"),
      shipped_by: gt ? clean(gt.innerText) : "",
      price: txt(".cart-product-price-buynow"),
      notes: [...el.querySelectorAll(".cart-product-price-tags-item")].map((n) => clean(n.innerText)),
      tax: txt(".cart-product-block-tax"),
      shipping: txt(".cart-product-ship-freight"),
      stock: txt(".cart-product-img-tip"),
      notice: txt(".cart-product-tip"),
      qty: (q(".comet-v2-input-number-input") || {}).value || "",
      checked: !!(cb && cb.checked),
      available: !/invalid/.test(el.className),
    });
  }
  return JSON.stringify(out);
})`

const cartPageJS = `(() => JSON.stringify({
  summary: (document.querySelector('.cart-summary') || {innerText: ""}).innerText,
  checkout: (document.querySelector('.cart-summary-button') || {innerText: ""}).innerText,
  bundle_href: ((document.querySelector("a[href*='BundleDeals']") || {}).href) || "",
}))()`

const bundlePageJS = `(() => JSON.stringify({
  present: !!document.querySelector('#subcart'),
  reminder: (document.querySelector('#subcart .bar-bottom-reminder-text') || {innerText: ""}).innerText,
}))()`

// FetchCart reads the signed-in account's cart, then its bundle-deals
// basket. It scrolls to load every line and never clicks anything.
func FetchCart(ctx context.Context) (*Cart, error) {
	if err := load(ctx, cartURL, cartReadyJS, 40*time.Second); err != nil {
		return nil, err
	}
	declared, err := loadAllLines(ctx)
	if err != nil {
		return nil, err
	}

	var rawLines, rawPage string
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(linesJS+`(".cart-main")`, &rawLines),
		chromedp.Evaluate(cartPageJS, &rawPage),
	); err != nil {
		return nil, err
	}
	var raws []rawLine
	if err := json.Unmarshal([]byte(rawLines), &raws); err != nil {
		return nil, fmt.Errorf("parsing cart lines: %w", err)
	}
	var page struct {
		Summary    string `json:"summary"`
		Checkout   string `json:"checkout"`
		BundleHref string `json:"bundle_href"`
	}
	if err := json.Unmarshal([]byte(rawPage), &page); err != nil {
		return nil, fmt.Errorf("parsing cart page: %w", err)
	}
	if len(raws) == 0 {
		return nil, fmt.Errorf("no cart lines found; the cart may be empty or the page layout may have changed (try --show)")
	}
	lines, err := parseLines(raws)
	if err != nil {
		return nil, err
	}
	sum, err := parseSummary(page.Summary, page.Checkout)
	if err != nil {
		return nil, err
	}

	c := &Cart{Declared: declared, Lines: lines, Summary: sum}
	checked := 0
	for _, l := range lines {
		if l.Available && l.Selected {
			checked++
		}
	}
	if declared != 0 && declared != len(lines) {
		c.Warnings = append(c.Warnings, fmt.Sprintf("the page says %d lines but %d were read", declared, len(lines)))
	}
	if sum.Selected != checked {
		c.Warnings = append(c.Warnings, fmt.Sprintf("the checkout button says %d selected but %d lines are ticked", sum.Selected, checked))
	}

	href := bundleURL
	if page.BundleHref != "" {
		href = page.BundleHref
	}
	c.Bundle = fetchBundle(ctx, href)
	return c, nil
}

// loadAllLines wheel-scrolls the page, which is what triggers the cart's
// lazy loading, until the line count stops growing or matches the header.
func loadAllLines(ctx context.Context) (int, error) {
	var declared, lastLines, still int
	for i := 0; i < 25; i++ {
		var raw string
		if err := chromedp.Run(ctx,
			chromedp.ActionFunc(func(c context.Context) error {
				return input.DispatchMouseEvent(input.MouseWheel, 700, 500).WithDeltaY(900).Do(c)
			}),
			chromedp.Sleep(1200*time.Millisecond),
			chromedp.Evaluate(cartCountJS, &raw),
		); err != nil {
			return 0, err
		}
		var n struct {
			Lines    int `json:"lines"`
			Declared int `json:"declared"`
		}
		if err := json.Unmarshal([]byte(raw), &n); err != nil {
			return 0, err
		}
		declared = n.Declared
		if n.Lines == lastLines {
			still++
		} else {
			still = 0
		}
		lastLines = n.Lines
		if (declared > 0 && n.Lines >= declared) || still >= 3 {
			break
		}
	}
	return declared, chromedp.Run(ctx, chromedp.Evaluate(`window.scrollTo(0, 0)`, nil))
}

func fetchBundle(ctx context.Context, href string) Bundle {
	if err := chromedp.Run(ctx, chromedp.Sleep(2*time.Second)); err != nil {
		return Bundle{Note: err.Error()}
	}
	if err := load(ctx, href, `!!document.querySelector('#subcart')`, 25*time.Second); err != nil {
		return Bundle{Note: "bundle-deals basket not reachable: " + err.Error()}
	}
	var rawLines, rawPage string
	if err := chromedp.Run(ctx,
		chromedp.Sleep(1500*time.Millisecond),
		chromedp.Evaluate(linesJS+`("#subcart")`, &rawLines),
		chromedp.Evaluate(bundlePageJS, &rawPage),
	); err != nil {
		return Bundle{Note: err.Error()}
	}
	var raws []rawLine
	var page struct {
		Present  bool   `json:"present"`
		Reminder string `json:"reminder"`
	}
	if err := json.Unmarshal([]byte(rawLines), &raws); err != nil {
		return Bundle{Note: "bundle basket unreadable: " + err.Error()}
	}
	if err := json.Unmarshal([]byte(rawPage), &page); err != nil || !page.Present {
		return Bundle{Note: "bundle-deals basket not found on the page"}
	}
	items, err := parseLines(raws)
	if err != nil {
		return Bundle{Note: "bundle basket unreadable: " + err.Error()}
	}
	return Bundle{
		Reachable: true,
		Needed:    parseBundleNeeded(page.Reminder),
		Status:    clean(page.Reminder),
		Items:     items,
	}
}
