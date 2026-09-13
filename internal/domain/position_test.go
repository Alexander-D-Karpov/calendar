package domain

import (
	"slices"
	"strings"
	"testing"
)

func TestKeyBetween(t *testing.T) {
	keys := []string{KeyBetween("", "")}
	for range 500 {
		keys = append(keys, KeyBetween(keys[len(keys)-1], ""))
	}
	for range 200 {
		keys = append([]string{KeyBetween("", keys[0])}, keys...)
	}
	for range 100 {
		mid := KeyBetween(keys[300], keys[301])
		keys = slices.Insert(keys, 301, mid)
	}
	for i := 1; i < len(keys); i++ {
		if keys[i-1] >= keys[i] {
			t.Fatalf("keys not increasing at %d: %q %q", i, keys[i-1], keys[i])
		}
	}
	for _, k := range keys {
		if strings.HasSuffix(k, "0") || len(k) > 128 {
			t.Fatalf("bad key %q", k)
		}
	}
	if got := KeyBetween("b", "a"); got <= "b" {
		t.Fatalf("inverted bounds = %q", got)
	}
}
