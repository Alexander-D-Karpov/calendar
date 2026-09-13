package domain

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestOptJSON(t *testing.T) {
	var p CalendarPatch
	if err := json.Unmarshal([]byte(`{"name":"Work","timezone":null}`), &p); err != nil {
		t.Fatal(err)
	}
	if !p.Name.Set || p.Name.Null || p.Name.V != "Work" {
		t.Fatalf("name = %+v", p.Name)
	}
	if !p.Timezone.Set || !p.Timezone.Null || p.Color.Set {
		t.Fatalf("timezone = %+v color = %+v", p.Timezone, p.Color)
	}
	var v ValidationError
	if _, ok := p.Timezone.Value(&v, "timezone"); ok || v.Err() == nil {
		t.Fatal("null must fail Value")
	}
}

func TestMatchETag(t *testing.T) {
	cases := map[string]bool{
		`"a"`:      true,
		`W/"a"`:    true,
		`"b", "a"`: true,
		`*`:        true,
		`"b"`:      false,
		``:         false,
		`a`:        false,
	}
	for h, want := range cases {
		if got := MatchETag(h, `"a"`); got != want {
			t.Errorf("MatchETag(%q) = %v", h, got)
		}
	}
	if CheckIfMatch("", `"a"`) != nil || !errors.Is(CheckIfMatch(`"b"`, `"a"`), ErrPrecondition) {
		t.Fatal("CheckIfMatch mismatch")
	}
}
