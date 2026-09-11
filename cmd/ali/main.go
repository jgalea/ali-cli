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

	"github.com/jgalea/ali-cli/internal/browser"
	"github.com/jgalea/ali-cli/internal/orders"
	toon "github.com/toon-format/toon-go"
)

const usage = `ali - read your AliExpress orders from the command line

Usage:
  ali login              sign in by hand, once, in a dedicated Chrome profile
  ali status             check whether that profile is still signed in
  ali orders [flags]     list your orders
  ali dump [file]        save the rendered order page, for fixing selectors

Flags:
  --limit N     how many orders to return (default 20, 0 for all loaded)
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
	if err := fs.Parse(rest); err != nil {
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
		if fs.NArg() > 0 {
			out = fs.Arg(0)
		}
		return dump(profile, out)
	default:
		return fmt.Errorf("unknown command %q (try `ali help`)", cmd)
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
		fmt.Println(header)
		for _, it := range o.Items {
			fmt.Println("    " + truncate(it.Title, 96))
		}
		fmt.Println("    " + o.URL)
	}
	return nil
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
