package auth

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

type Scope string

const (
	ScopeAccountRead    Scope = "account:read"
	ScopeAccountWrite   Scope = "account:write"
	ScopeCalendarsRead  Scope = "calendars:read"
	ScopeCalendarsWrite Scope = "calendars:write"
	ScopeTodosRead      Scope = "todos:read"
	ScopeTodosWrite     Scope = "todos:write"
	ScopeSharesRead     Scope = "shares:read"
	ScopeSharesWrite    Scope = "shares:write"
	ScopeImportsWrite   Scope = "imports:write"
	ScopeExportsRead    Scope = "exports:read"
	ScopeSyncWrite      Scope = "sync:write"
)

var AllScopes = []Scope{
	ScopeAccountRead,
	ScopeAccountWrite,
	ScopeCalendarsRead,
	ScopeCalendarsWrite,
	ScopeTodosRead,
	ScopeTodosWrite,
	ScopeSharesRead,
	ScopeSharesWrite,
	ScopeImportsWrite,
	ScopeExportsRead,
	ScopeSyncWrite,
}

func ParseScopes(in []string) ([]Scope, error) {
	var out []Scope
	for _, s := range in {
		sc := Scope(strings.ToLower(strings.TrimSpace(s)))
		if sc == "" {
			continue
		}
		if !slices.Contains(AllScopes, sc) {
			return nil, fmt.Errorf("%w: unknown scope %q", domain.ErrInvalid, s)
		}
		if !slices.Contains(out, sc) {
			out = append(out, sc)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: at least one scope is required", domain.ErrInvalid)
	}
	slices.Sort(out)
	return out, nil
}

func Allows(granted []Scope, need Scope) bool {
	if slices.Contains(granted, need) {
		return true
	}
	resource, action, ok := strings.Cut(string(need), ":")
	return ok && action == "read" && slices.Contains(granted, Scope(resource+":write"))
}

func ScopeStrings(scopes []Scope) []string {
	out := make([]string, len(scopes))
	for i, s := range scopes {
		out[i] = string(s)
	}
	return out
}
