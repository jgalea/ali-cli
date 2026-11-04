package catalog

import "testing"

const itemText = `Related items
Turbo Jet Fan 130000 RPM, 10-Speed with Display
  4.8  
1160 Reviews
  ౹  
10,000+ sold
 
19,57€
 Save 4,07€
30-day lowest price before the discount. 23,64€ 
Price includes VAT | No import charges
Color Name: FT08
Customer Reviews (1160)
Sold By
Example Auto Store
(Trader)
Service commitment
Free shipping ·  Ship from Poland 
Fastest delivery in 7 days (80.9% ≤ 9 days)
Quantity
Example Auto Store
96.0% Positive Feedback | 446 Followers`

func TestParseItem(t *testing.T) {
	it, err := parseItem(itemText, nil)
	if err != nil {
		t.Fatal(err)
	}
	if it.Price != 19.57 || it.Lowest30Days != 23.64 || !it.VATIncluded || it.ImportCharges != "none" ||
		it.ShipsFrom != "Poland" || it.Reviews != "1160" || it.Sold != "10,000+" ||
		it.Store != "Example Auto Store" || it.StorePositive != "96.0%" || it.Bundle {
		t.Fatalf("unexpected parse: %+v", it)
	}
}

func TestParseCard(t *testing.T) {
	r, ok := parseCard("//x.aliexpress.com/item/1005010513544564.html?a=b",
		"Ad\nBlower\n12,89€\n12,89€\n4.5\n100K+ sold\nFree shipping", nil)
	if !ok || r.ID != "1005010513544564" || r.Price != 12.89 || r.Rating != "4.5" || r.Sold != "100K+" || r.Badge != "Free shipping" {
		t.Fatalf("unexpected card: %+v", r)
	}
	if _, ok := parseCard("/item/1005010513544564.html", "no price here", nil); ok {
		t.Fatal("a card without a price should be rejected")
	}
}

func TestSearchURL(t *testing.T) {
	u, err := SearchURL("air blower", "pl")
	if err != nil || u != "https://es.aliexpress.com/w/wholesale-air-blower.html?SearchText=air+blower&shpf_co=PL" {
		t.Fatalf("got %q, %v", u, err)
	}
	if _, err := SearchURL("x", "poland"); err == nil {
		t.Fatal("expected an error for a bad --ships-from")
	}
}
