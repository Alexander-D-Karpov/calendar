package search

import (
	"strings"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

func build(t *testing.T, raw string) (string, []any) {
	t.Helper()
	now := today.Add(12 * time.Hour)
	return Build(Params{Owner: domain.NewID(), Query: Parse(raw, today), Now: now, Today: today, Limit: 20})
}

func TestBuildParameterizes(t *testing.T) {
	sql, args := build(t, `standup in:Work after:2026-09-01 priority:>=2 "drop table"`)
	for _, leak := range []string{"standup", "Work", "drop table", "2026-09-01"} {
		if strings.Contains(sql, leak) {
			t.Fatalf("SQL contains user input %q:\n%s", leak, sql)
		}
	}
	if len(args) < 5 {
		t.Fatalf("args = %v", args)
	}
	if !strings.Contains(sql, "UNION ALL") {
		t.Fatal("a query for both kinds must union them")
	}
}

func TestBuildTypeFilter(t *testing.T) {
	sql, _ := build(t, "type:todo gpu")
	if strings.Contains(sql, "FROM events") || !strings.Contains(sql, "FROM todos") {
		t.Fatalf("type:todo must query todos only:\n%s", sql)
	}
	sql, _ = build(t, "type:event gpu")
	if strings.Contains(sql, "FROM todos") || !strings.Contains(sql, "FROM events") {
		t.Fatalf("type:event must query events only:\n%s", sql)
	}
}

func TestBuildImpossibleFiltersRemoveTheKind(t *testing.T) {
	sql, _ := build(t, "type:todo is:done standup")
	if strings.Contains(sql, "FROM events") || strings.Contains(sql, "AND false") {
		t.Fatalf("is:done must query todos only:\n%s", sql)
	}
	sql, _ = build(t, "type:event has:location gpu")
	if strings.Contains(sql, "FROM todos") || strings.Contains(sql, "AND false") {
		t.Fatalf("has:location must query events only:\n%s", sql)
	}
}

func TestNarrowDropsBlockedKinds(t *testing.T) {
	todosOnly := narrow(Parse("is:done standup", today))
	if todosOnly.Type != TypeTodo || len(todosOnly.Warnings) != 1 {
		t.Fatalf("is:done = %q %v", todosOnly.Type, todosOnly.Warnings)
	}
	eventsOnly := narrow(Parse("has:location gpu", today))
	if eventsOnly.Type != TypeEvent || len(eventsOnly.Warnings) != 1 {
		t.Fatalf("has:location = %q %v", eventsOnly.Type, eventsOnly.Warnings)
	}
	none := narrow(Parse("is:done has:location x", today))
	if none.Type != typeNone || len(none.Warnings) != 1 {
		t.Fatalf("both = %q %v", none.Type, none.Warnings)
	}
	kept := narrow(Parse("standup", today))
	if kept.Type != TypeAny || len(kept.Warnings) != 0 {
		t.Fatalf("plain = %q %v", kept.Type, kept.Warnings)
	}
}

func TestBuildWithoutText(t *testing.T) {
	sql, _ := build(t, "is:overdue")
	if strings.Contains(sql, "websearch_to_tsquery") {
		t.Fatal("a filter-only query must not build a tsquery")
	}
	if !strings.Contains(sql, "t.due_date <") {
		t.Fatalf("is:overdue must compare the due date:\n%s", sql)
	}
}

func TestCompareOp(t *testing.T) {
	for _, op := range []string{">", ">=", "<", "<=", "="} {
		if compareOp(op) != op {
			t.Errorf("compareOp(%q) changed it", op)
		}
	}
	if compareOp("; DROP") != "=" {
		t.Fatal("an unknown operator must fall back to equality")
	}
}
