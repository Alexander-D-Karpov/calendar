package auth

import (
	"errors"
	"slices"
	"testing"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

func TestParseScopes(t *testing.T) {
	got, err := ParseScopes([]string{" Todos:Write", "calendars:read", "todos:write", ""})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ScopeStrings(got), []string{"calendars:read", "todos:write"}) {
		t.Fatalf("scopes = %v", got)
	}
	for _, bad := range [][]string{nil, {""}, {"admin"}, {"calendars:read", "events:delete"}} {
		if _, err := ParseScopes(bad); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("ParseScopes(%v) err = %v", bad, err)
		}
	}
}

func TestAllows(t *testing.T) {
	granted := []Scope{ScopeCalendarsWrite, ScopeTodosRead, ScopeImportsWrite}
	cases := map[Scope]bool{
		ScopeCalendarsWrite: true,
		ScopeCalendarsRead:  true,
		ScopeTodosRead:      true,
		ScopeTodosWrite:     false,
		ScopeImportsWrite:   true,
		ScopeExportsRead:    false,
		ScopeAccountRead:    false,
	}
	for need, want := range cases {
		if got := Allows(granted, need); got != want {
			t.Errorf("Allows(%s) = %v, want %v", need, got, want)
		}
	}
}
