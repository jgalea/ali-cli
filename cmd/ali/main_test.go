package main

import "testing"

func TestSafeStripsControlCharacters(t *testing.T) {
	got := safe("shop\x1b[31mred\x1b]0;title\x07\u009b2J name\n\tend")
	want := "shop[31mred]0;title2J name\n\tend"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
