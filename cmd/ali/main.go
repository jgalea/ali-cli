// Command ali reads your own AliExpress order history from the command
// line, by driving a real Chrome profile you signed into once.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/jgalea/ali-cli/internal/browser"
	"github.com/jgalea/ali-cli/internal/catalog"
	"github.com/jgalea/ali-cli/internal/orders"
	toon "github.com/toon-format/toon-go"
)

const usage = `ali - read your AliExpress orders from the command line

Usage:
  ali login              sign in by hand, once, in a dedicated Chrome profile
  ali status             check whether that profile is still signed in
  ali orders [flags]     list your orders
  ali item <id|url>      price, shipping, VAT and seller details for one product
  ali search <query>     list search results
  ali cart               read your shopping cart and its bundle-deals basket
  ali dump [file]        save the rendered order page, for fixing selectors

Flags:
  --limit N     how many orders or results to return (default 20, 0 for all loaded)
  --ships-from  search only: EU or a two-letter country code (PL, ES, DE...)
  --grep TEXT   only orders with an item title matching TEXT
  --json        emit JSON
  --toon        emit TOON instead of JSON (fewer tokens, for agents)
  --expand      open each multi-item order for its item titles (slower)
  --show        run with a visible window (default for login)

Environment:
  ALI_CONFIG_DIR   where the browser profile lives (default ~/.ali)
  ALI_CHROME       path to the Chrome or Chromium binary
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ali: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Print(usage)
		return nil
	}

	cmd, rest := args[0], args[1:]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	limit := fs.Int("limit", 20, "how many orders to return")
	grep := fs.String("grep", "", "only orders with an item title matching this")
	asJSON := fs.Bool("json", false, "emit JSON")
	asTOON := fs.Bool("toon", false, "emit TOON")
	show := fs.Bool("show", false, "run with a visible window")
	expand := fs.Bool("expand", false, "open each multi-item order for its item titles")
	shipsFrom := fs.String("ships-from", "", "search only: EU or a two-letter country code")
	pos, err := parseInterspersed(fs, rest)
	if err != nil {
		return err
	}

	profile := filepath.Join(browser.ConfigDir(), "profile")

	switch cmd {
	case "login":
		return login(profile)
	case "status":
		return status(profile, *show, *asJSON || *asTOON, *asTOON)
	case "orders":
		return list(profile, *limit, *grep, *expand, *show, *asJSON, *asTOON)
	case "dump":
		out := "order-page.html"
		if len(pos) > 0 {
			out = pos[0]
		}
		return dump(profile, out)
	case "item":
		if len(pos) != 1 {
			return fmt.Errorf("usage: ali item <id|url>")
		}
		return item(profile, pos[0], *show, *asJSON, *asTOON)
	case "cart":
		return cart(profile, *show, *asJSON, *asTOON)
	case "search":
		if len(pos) == 0 {
			return fmt.Errorf("usage: ali search <query>")
		}
		return search(profile, strings.Join(pos, " "), *shipsFrom, *limit, *show, *asJSON, *asTOON)
	default:
		return fmt.Errorf("unknown command %q (try `ali help`)", cmd)
	}
}

// parseInterspersed lets flags follow positional arguments, so both
// `ali item --json 123` and `ali item 123 --json` work.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func session(profile string, headless bool, timeout time.Duration) (*browser.Session, error) {
	return browser.Open(context.Background(), browser.Options{
		ProfileDir: profile,
		Headless:   headless,
		Timeout:    timeout,
	})
}

func login(profile string) error {
	s, err := session(profile, false, 0)
	if err != nil {
		return err
	}
	defer s.Close()

	fmt.Fprintln(os.Stderr, "A Chrome window is opening. Sign in to AliExpress there.")
	fmt.Fprintln(os.Stderr, "No clock on this: it waits, and picks the session up on its own.")
	fmt.Fprintln(os.Stderr, "Press Enter here once you are in if it has not noticed yet.")

	done := make(chan struct{})
	go func() {
		bufio.NewReader(os.Stdin).ReadString('\n')
		close(done)
	}()

	if err := orders.WaitForLogin(s.Ctx, done); err != nil {
		return err
	}
	n, err := browser.SaveCookies(s.Ctx, browser.CookiePath())
	if err != nil {
		return fmt.Errorf("signed in, but saving the session failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Signed in. Saved %d cookies to %s, so `ali orders` works from now on.\n", n, browser.CookiePath())
	return nil
}

func status(profile string, show, structured, asTOON bool) error {
	s, err := session(profile, !show, 90*time.Second)
	if err != nil {
		return err
	}
	defer s.Close()

	restore(s)

	ok, err := orders.LoggedIn(s.Ctx)
	if err != nil {
		return err
	}
	if structured {
		return emit(map[string]bool{"signed_in": ok}, asTOON)
	}
	if ok {
		fmt.Println("signed in")
		return nil
	}
	fmt.Println("signed out (run `ali login`)")
	return nil
}

func list(profile string, limit int, grep string, expand, show, asJSON, asTOON bool) error {
	s, err := session(profile, !show, 5*time.Minute)
	if err != nil {
		return err
	}
	defer s.Close()

	restore(s)

	got, err := orders.List(s.Ctx, limit)
	if err != nil {
		return err
	}
	if expand {
		got, err = orders.Expand(s.Ctx, got)
		if err != nil {
			return err
		}
	}
	if grep != "" {
		got = filter(got, grep)
	}
	if asJSON || asTOON {
		return emit(got, asTOON)
	}
	if len(got) == 0 {
		fmt.Fprintln(os.Stderr, "no orders found")
		return nil
	}
	for _, o := range got {
		header := o.ID
		if o.Date != "" {
			header += "  " + o.Date
		}
		if o.Total != "" {
			header += "  " + o.Total
		}
		if o.Status != "" {
			header += "  [" + o.Status + "]"
		}
		say(header)
		for _, it := range o.Items {
			say("    " + truncate(it.Title, 96))
		}
		say("    " + o.URL)
	}
	return nil
}

func item(profile, arg string, show, asJSON, asTOON bool) error {
	id, err := catalog.ItemID(arg)
	if err != nil {
		return err
	}
	s, err := session(profile, !show, 2*time.Minute)
	if err != nil {
		return err
	}
	defer s.Close()

	restore(s)

	it, err := catalog.FetchItem(s.Ctx, id)
	if err != nil {
		return err
	}
	if asJSON || asTOON {
		return emit(it, asTOON)
	}
	say(it.Title)
	price := fmt.Sprintf("%s %.2f", it.Currency, it.Price)
	if it.Lowest30Days > 0 {
		price += fmt.Sprintf("  (30-day lowest %.2f)", it.Lowest30Days)
	}
	say("  price:     " + price)
	tax := "VAT not stated"
	if it.VATIncluded {
		tax = "VAT included"
	}
	switch it.ImportCharges {
	case "none":
		tax += ", no import charges"
	case "apply":
		tax += ", import charges apply"
	}
	say("  tax:       " + tax)
	if it.ShipsFrom != "" {
		say("  ships from: " + it.ShipsFrom)
	}
	if it.Delivery != "" {
		say("  delivery:  " + it.Delivery)
	}
	if it.Rating != "" {
		sayf("  rating:    %s (%s reviews)\n", it.Rating, it.Reviews)
	}
	if it.Sold != "" {
		say("  sold:      " + it.Sold)
	}
	if it.Store != "" {
		store := it.Store
		if it.StorePositive != "" {
			store += " (" + it.StorePositive + " positive)"
		}
		say("  store:     " + store)
	}
	for _, v := range it.Variants {
		line := fmt.Sprintf("  %s: %s", v.Name, strings.Join(v.Options, ", "))
		if v.Count > len(v.Options) {
			line += fmt.Sprintf(" (%d options)", v.Count)
		}
		say(line)
	}
	if it.Bundle {
		say("  bundle deals: yes")
	}
	say("  " + it.URL)
	return nil
}

func search(profile, query, shipsFrom string, limit int, show, asJSON, asTOON bool) error {
	if limit <= 0 {
		limit = 60
	}
	s, err := session(profile, !show, 2*time.Minute)
	if err != nil {
		return err
	}
	defer s.Close()

	restore(s)

	got, err := catalog.Search(s.Ctx, query, shipsFrom, limit)
	if err != nil {
		return err
	}
	if asJSON || asTOON {
		return emit(got, asTOON)
	}
	if len(got) == 0 {
		fmt.Fprintln(os.Stderr, "no results")
		return nil
	}
	for _, r := range got {
		line := fmt.Sprintf("%s  %s %.2f", r.ID, r.Currency, r.Price)
		if r.Rating != "" {
			line += "  " + r.Rating + "*"
		}
		if r.Sold != "" {
			line += "  " + r.Sold + " sold"
		}
		if r.ShipsFrom != "" {
			line += "  from " + r.ShipsFrom
		}
		if r.Badge != "" {
			line += "  [" + r.Badge + "]"
		}
		if r.Bundle {
			line += "  [bundle deals]"
		}
		say(line)
		say("    " + truncate(r.Title, 96))
	}
	return nil
}

func cart(profile string, show, asJSON, asTOON bool) error {
	s, err := session(profile, !show, 4*time.Minute)
	if err != nil {
		return err
	}
	defer s.Close()

	restore(s)

	c, err := catalog.FetchCart(s.Ctx)
	if err != nil {
		return err
	}
	if asJSON || asTOON {
		return emit(c, asTOON)
	}
	avail := 0
	for _, l := range c.Lines {
		if l.Available {
			avail++
		}
	}
	sayf("Cart: %d lines (%d available, %d unavailable), %d selected\n", len(c.Lines), avail, len(c.Lines)-avail, c.Summary.Selected)
	for _, l := range c.Lines {
		printCartLine(l)
	}
	sum := c.Summary
	shipping := sum.Shipping
	if sum.ShippingFree {
		shipping = "free"
	}
	sayf("\nSelected %d: subtotal EUR %.2f, shipping %s, estimated total EUR %.2f\n", sum.Selected, sum.Subtotal, shipping, sum.EstimatedTotal)
	say("\nBundle-deals basket")
	if !c.Bundle.Reachable {
		say("  not read: " + c.Bundle.Note)
	} else {
		if c.Bundle.Status != "" {
			sayf("  status: %s (%d items needed)\n", c.Bundle.Status, c.Bundle.Needed)
		}
		if len(c.Bundle.Items) == 0 {
			say("  empty")
		}
		for _, l := range c.Bundle.Items {
			printCartLine(l)
		}
	}
	for _, w := range c.Warnings {
		fmt.Fprintln(os.Stderr, "warning: "+w)
	}
	return nil
}

func printCartLine(l catalog.Line) {
	mark := "[ ]"
	switch {
	case !l.Available:
		mark = "[-]"
	case l.Selected:
		mark = "[x]"
	}
	head := fmt.Sprintf("%s %s", mark, l.ID)
	if l.Available {
		head += fmt.Sprintf("  %s %.2f", l.Currency, l.UnitPrice)
	}
	if l.Quantity > 1 {
		head += fmt.Sprintf(" x%d", l.Quantity)
	}
	if l.StockWarning != "" {
		head += "  (" + l.StockWarning + ")"
	}
	say(head)
	say("    " + truncate(l.Title, 96))
	if l.Variant != "" {
		say("    variant: " + l.Variant)
	}
	if l.Store != "" {
		say("    store:   " + l.Store)
	}
	tax := ""
	if l.VATIncluded {
		tax = "VAT included"
	}
	switch l.ImportCharges {
	case "none":
		tax += ", no import charges"
	case "apply":
		tax += ", import charges apply"
	}
	if tax = strings.TrimPrefix(tax, ", "); tax != "" {
		say("    tax:     " + tax)
	}
	if l.Notice != "" {
		say("    notice:  " + l.Notice)
	}
}

func dump(profile, out string) error {
	s, err := session(profile, true, 2*time.Minute)
	if err != nil {
		return err
	}
	defer s.Close()

	restore(s)

	html, err := orders.DumpHTML(s.Ctx)
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, []byte(html), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d bytes)\n", out, len(html))
	return nil
}

// restore puts the saved session back before any page load. A missing
// or stale jar is not fatal here; the caller reports "not signed in".
func restore(s *browser.Session) {
	if _, err := browser.LoadCookies(s.Ctx, browser.CookiePath()); err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "warning: could not restore the saved session: "+err.Error())
	}
}

func filter(in []orders.Order, q string) []orders.Order {
	q = strings.ToLower(q)
	var out []orders.Order
	for _, o := range in {
		for _, it := range o.Items {
			if strings.Contains(strings.ToLower(it.Title), q) {
				out = append(out, o)
				break
			}
		}
	}
	return out
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func safe(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, s)
}

func say(s string) {
	fmt.Println(safe(s))
}

func sayf(format string, a ...any) {
	fmt.Print(safe(fmt.Sprintf(format, a...)))
}

func emit(v any, asTOON bool) error {
	if asTOON {
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		var generic any
		if err := json.Unmarshal(raw, &generic); err != nil {
			return err
		}
		s, err := toon.MarshalString(generic)
		if err != nil {
			return err
		}
		_, err = fmt.Println(s)
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
