<h1 align="center">ali-cli</h1>

<p align="center">
  <a href="go.mod"><img src="https://img.shields.io/badge/GO-1.26%2B-00ADD8?style=for-the-badge&logo=go&logoColor=white" alt="Go"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/LICENSE-MIT-5C9E31?style=for-the-badge" alt="License"></a>
  <img src="https://img.shields.io/badge/SINGLE%20BINARY-no%20runtime%20deps-brightgreen?style=for-the-badge" alt="Single binary">
</p>

<p align="center"><strong>Unofficial, agent-friendly CLI for your own AliExpress account. Sign in once by hand, then read your orders, product pages, search results and shopping cart as text, JSON or TOON.</strong></p>

---

## Why this exists

AliExpress has no consumer API. The Open Platform covers affiliates, sellers and dropshippers, and none of it can answer "what did I order in March". The community tools that tried are either dead or wrap the affiliate endpoints.

So `ali` does the one thing that still works: it drives a real Chrome profile that you signed into yourself, and reads the order list out of the page. There is no scripted login, no stored password, and no attempt to defeat a captcha. You sign in by hand, once, and the profile keeps the session.

That makes it useful for the things the app is bad at: searching your own purchase history, feeding orders to a script, or letting an agent answer "which version of this part did I actually order".

## Install

```
go install github.com/jgalea/ali-cli/cmd/ali@latest
```

Needs Go 1.26+ and a local Chrome or Chromium. It runs on macOS, Linux and Windows.

On Windows, `~` below means your user folder (`%USERPROFILE%`), so the profile lives in `%USERPROFILE%\.ali`. Chrome is found in the usual install locations on each OS; if yours lives elsewhere, point `ALI_CHROME` at it.

## Use

```
ali login              # opens a window; sign in yourself
ali status             # is that profile still signed in?
ali orders             # recent orders, one block each
ali orders --limit 50 --json
ali orders --limit 0 --expand   # everything, titles filled in
ali orders --grep wax  # only orders with a matching item title
ali item 1005011605911641       # one product, by id or URL
ali search "air blower" --ships-from EU --limit 10
ali cart                        # cart lines, totals and the bundle-deals basket
```

Every command takes `--json`, or `--toon` for [TOON](https://github.com/toon-format/toon-go) when you are feeding an LLM and want fewer tokens.

```
$ ali orders --grep wax
1234567890123456  12 Aug 2026  EUR 4.21  [Completed]
    Example item title as it appears in your order
    https://www.aliexpress.com/p/order/detail.html?orderId=1234567890123456
```

(Illustrative shape, not a real order.)

## Products and search

`ali item` prints the title, price, 30-day lowest price, whether VAT is included, whether import charges apply, ships-from country, delivery estimate, rating, reviews, units sold, store name with its positive-feedback rate, variants, and the bundle-deals tag. `ali search` lists id, title, price, rating, units sold, the free-shipping or Choice badge and the bundle tag. Both use the es.aliexpress.com storefront and the saved session, so prices are the ones your account sees. Both read only: nothing is clicked except page navigation.

```
$ ali item 1005011605911641
Foursun 130000 RPM Turbo Violent Fan, 10-Speed with Display, ...
  price:     EUR 18.03  (30-day lowest 21.93)
  tax:       VAT included, no import charges
  ships from: Poland
  rating:    4.8 (1160 reviews)
  store:     Foursun Auto Store (96.0% positive)

$ ali search "air blower" --ships-from PL --limit 3
1005013370091652  EUR 29.13  12 sold  from PL
    130000RPM Powerful Cordless Turbo Air Blower, 2-in-1 Electric Air Duster ...
```

`--ships-from` takes `EU` or a two-letter country code and passes AliExpress's own "Ship from" filter in the URL, so the results are filtered by the site. Result cards do not print the origin, so the country shown in the list is the one you asked for. Output is English text from the storefront; `--json` and `--toon` work as with `orders`.

## Cart

`ali cart` reads the cart page and prints every line: title, variant, store, unit price, whether VAT is included, whether import charges apply, any stock warning, whether the line is ticked for checkout, and the item id. Unavailable lines are listed with the reason the site gives. The summary shows subtotal, shipping, estimated total and the number of selected items. It then opens the separate bundle-deals basket and shows its items and how many more are needed; if that basket cannot be reached, the output says so. Read only: it scrolls the page to load every line and reads checkbox state, and clicks nothing.

```
$ ali cart
Cart: 4 lines (3 available, 1 unavailable), 2 selected
[x] 1005000000000001  EUR 18.03  (Only 1 left)
    Example turbo fan, 10 speeds, with display
    variant: Blue/Poland
    store:   Example Store
    tax:     VAT included, no import charges
[x] 1005000000000002  EUR 9.89
    Example 4-piece RC tire set
    store:   Sample Hobby Store
    tax:     VAT included, import charges apply
[ ] 1005000000000003  EUR 2.81
    Example self-adhesive film
[-] 
    Example retired item
    notice:  Item unavailable

Selected 2: subtotal EUR 27.92, shipping free, estimated total EUR 27.92

Bundle-deals basket
  status: Add 3 from "Toys" for duty covered & free shipping (3 items needed)
[ ]   EUR 6.28  (Almost sold out)
    Example steering part
    variant: 16005
```

The page renders in Spanish or English depending on the session. Both are understood; a label it does not know makes the command fail with the text it could not read, so a changed storefront shows up as an error rather than as wrong numbers.

## How it works

`ali login` starts Chrome with a dedicated profile directory (`~/.ali/profile`, or `ALI_CONFIG_DIR`) and watches the address bar until it stops sitting on the login screen. It never reloads the page while you are typing into it.

AliExpress issues its auth cookies as session cookies, which Chrome throws away on exit, so a browser profile alone forgets the login every time. After a confirmed sign-in the cookie jar is read over the DevTools protocol and written to `~/.ali/cookies.json` (mode 600), then put back before each later run. Treat that file as a password: anyone holding it holds your account session. Delete it to sign out.

`ali orders` reopens that profile headless, loads the order list, and keeps pressing the page's own "View orders" button until the list stops growing. Each order is read from AliExpress's own class names (`order-item`, `order-item-store-name`, `order-item-content-opt-price-total`), which are stable enough to target directly. Prices are rendered one character per span, presumably to frustrate exactly this, which `innerText` reassembles anyway.

An order of several items shows only thumbnails in the list, with no titles. `--expand` opens each of those orders on its detail page to fill the titles in, at the cost of a page load each.

It is still scraping, so it will break. When it does, `ali dump orders.html` saves the rendered page so the selectors can be fixed against real markup.

## Configuration

| Variable | Default | What it does |
|---|---|---|
| `ALI_CONFIG_DIR` | `~/.ali` | Where the browser profile lives |
| `ALI_CHROME` | autodetected | Path to the Chrome or Chromium binary |

The saved session lives at `$ALI_CONFIG_DIR/cookies.json`. It is never written into the repository or anywhere else.

## Limits

- Reads only. It never places, cancels or pays for anything.
- One account, the one signed into the profile.
- The order list is what the web app shows. Very old orders that the site has stopped listing are not reachable.
- Headless runs occasionally trip AliExpress's bot checks. Pass `--show` to run with a visible window if a command comes back empty.

## License

MIT
