package catalog

import (
	"strings"
	"testing"
)

func rawES() rawLine {
	return rawLine{
		Title: "Widget Pro 5000, 10 velocidades", Href: "https://www.aliexpress.com/item/1005000000000001.html?mp=1&x=1234567890123",
		Variant: "Azul/Polonia", Store: "Example Store", ShippedBy: "Enviado por vendedores locales",
		Price: "18,03€", Notes: []string{"Promo · -17% solo ahora"},
		Tax: "Precio con IVA incluido | Sin gastos de importación", Shipping: "Envío gratis",
		Stock: "Solo quedan 1", Qty: "2", Checked: true, Available: true,
	}
}

func TestParseLineSpanish(t *testing.T) {
	l, err := parseLine(rawES())
	if err != nil {
		t.Fatal(err)
	}
	if l.ID != "1005000000000001" || l.UnitPrice != 18.03 || l.Quantity != 2 || !l.VATIncluded ||
		l.ImportCharges != "none" || !l.Selected || l.StockWarning != "Solo quedan 1" || l.Store != "Example Store" {
		t.Fatalf("unexpected line: %+v", l)
	}
}

func TestParseLineEnglish(t *testing.T) {
	r := rawES()
	r.Tax = "Price includes VAT | Import charges will apply"
	r.Stock = "Only 1 left"
	r.Checked = false
	l, err := parseLine(r)
	if err != nil {
		t.Fatal(err)
	}
	if !l.VATIncluded || l.ImportCharges != "apply" || l.Selected {
		t.Fatalf("unexpected line: %+v", l)
	}
}

func TestParseLineChangedLabelFailsLoudly(t *testing.T) {
	r := rawES()
	r.Tax = "Prix TVA incluse | Frais d'importation applicables"
	_, err := parseLine(r)
	if err == nil || !strings.Contains(err.Error(), "unrecognised tax label") {
		t.Fatalf("expected a loud failure on an unknown label, got %v", err)
	}
}

func TestParseLineUnavailableNeedsNoPrice(t *testing.T) {
	l, err := parseLine(rawLine{Title: "Gone item", Notice: "Artículo no disponible"})
	if err != nil || l.Available || l.UnitPrice != 0 {
		t.Fatalf("got %+v, %v", l, err)
	}
	if _, err := parseLine(rawLine{Title: "Priceless", Available: true}); err == nil {
		t.Fatal("an available line with no price should fail")
	}
}

func TestParseLinesEmptyInputIsNotAFailureHere(t *testing.T) {
	got, err := parseLines(nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

const summaryES = `Resumen
Casi agotado
Solo quedan 8
Subtotal
42,71€
Envío
Gratis
¡1,99€ ahorrado! Visita la página de pago
Estimación total
42,71€`

const summaryEN = `Summary
Subtotal
1.042,50€
Shipping
3,99€
Estimated total
1.046,49€`

func TestParseSummary(t *testing.T) {
	s, err := parseSummary(summaryES, "Continuar (3)")
	if err != nil {
		t.Fatal(err)
	}
	if s.Subtotal != 42.71 || s.EstimatedTotal != 42.71 || !s.ShippingFree || s.Selected != 3 {
		t.Fatalf("unexpected summary: %+v", s)
	}
	s, err = parseSummary(summaryEN, "Checkout (12)")
	if err != nil {
		t.Fatal(err)
	}
	if s.Subtotal != 1042.5 || s.EstimatedTotal != 1046.49 || s.ShippingFree || s.Shipping != "3,99€" || s.Selected != 12 {
		t.Fatalf("unexpected summary: %+v", s)
	}
}

func TestParseSummaryChangedLabelFailsLoudly(t *testing.T) {
	changed := strings.Replace(summaryES, "Estimación total", "Total estimado final", 1)
	if _, err := parseSummary(changed, "Continuar (3)"); err == nil {
		t.Fatal("expected an error when the total label is not in the map")
	}
	if _, err := parseSummary("", ""); err == nil {
		t.Fatal("expected an error on an empty summary")
	}
}

func TestParseBundleNeeded(t *testing.T) {
	cases := map[string]int{
		`Add 3 from "Toys" for duty covered & free shipping`:            3,
		`Añade 2 de "Juguetes" para compensar aranceles y envío gratis`: 2,
		`Free shipping unlocked`:                                        0,
	}
	for in, want := range cases {
		if got := parseBundleNeeded(in); got != want {
			t.Errorf("%q: got %d, want %d", in, got, want)
		}
	}
}
