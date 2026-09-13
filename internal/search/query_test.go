package search

import (
	"slices"
	"strings"
	"testing"
	"time"
)

var today = time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)

func TestParseTerms(t *testing.T) {
	q := Parse(`standup "team sync" -cancelled`, today)
	if len(q.Terms) != 3 {
		t.Fatalf("terms = %+v", q.Terms)
	}
	if q.Terms[1].Text != "team sync" || !q.Terms[1].Phrase || !q.Terms[2].Negate {
		t.Fatalf("terms = %+v", q.Terms)
	}
	if got := q.TSQuery(); got != `standup "team sync" -cancelled` {
		t.Fatalf("tsquery = %q", got)
	}
	if q.Prefix() != "standup" {
		t.Fatalf("prefix = %q", q.Prefix())
	}
	if !q.Text() || q.Empty() {
		t.Fatal("a query with words must count as text")
	}
}

func TestParseFilters(t *testing.T) {
	q := Parse("type:todo in:Work is:overdue has:checks after:today before:+2w priority:>=2 gpu", today)
	if q.Type != TypeTodo || !slices.Equal(q.In, []string{"work"}) || !slices.Equal(q.Is, []string{"overdue"}) {
		t.Fatalf("query = %+v", q)
	}
	if !slices.Equal(q.Has, []string{"checks"}) || q.After == nil || !q.After.Equal(today) {
		t.Fatalf("query = %+v", q)
	}
	if q.Before == nil || !q.Before.Equal(today.AddDate(0, 0, 14)) {
		t.Fatalf("before = %v", q.Before)
	}
	if q.Priority == nil || q.Priority.Op != ">=" || q.Priority.Value != 2 {
		t.Fatalf("priority = %+v", q.Priority)
	}
	if len(q.Terms) != 1 || q.Terms[0].Text != "gpu" || len(q.Warnings) != 0 {
		t.Fatalf("terms = %+v warnings = %v", q.Terms, q.Warnings)
	}
}

func TestParseBadFilters(t *testing.T) {
	q := Parse("is:nonsense priority:nine after:soon type:person meeting", today)
	if len(q.Warnings) != 4 {
		t.Fatalf("warnings = %v", q.Warnings)
	}
	if len(q.Terms) != 5 {
		t.Fatalf("unparsed filters must fall back to words: %+v", q.Terms)
	}
	if q.Type != TypeAny || q.Priority != nil || q.After != nil {
		t.Fatalf("query = %+v", q)
	}
}

func TestTSQuerySanitizes(t *testing.T) {
	q := Parse(`a&b !c "d|e" <f>`, today)
	got := q.TSQuery()
	for _, bad := range []string{"&", "!", "|", "<", ">"} {
		if strings.Contains(got, bad) {
			t.Fatalf("tsquery leaks %q: %s", bad, got)
		}
	}
	if Parse(`" "`, today).TSQuery() != "" {
		t.Fatal("a blank phrase must produce nothing")
	}
	if !Parse("-only", today).Empty() == false {
		t.Fatal("a negated-only query is not empty but has no text")
	}
	if Parse("-only", today).Text() {
		t.Fatal("a negated-only query has no positive text")
	}
}

func TestParseLimits(t *testing.T) {
	long := strings.Repeat("word ", 40)
	q := Parse(long, today)
	if len(q.Terms) != maxTerms || len(q.Warnings) == 0 {
		t.Fatalf("terms = %d warnings = %v", len(q.Terms), q.Warnings)
	}
	if Parse(strings.Repeat("x", maxQueryLen+50), today).Raw != strings.Repeat("x", maxQueryLen) {
		t.Fatal("the raw query must be cut")
	}
}

func TestParseEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", `""`} {
		if !Parse(in, today).Empty() {
			t.Errorf("Parse(%q) must be empty", in)
		}
	}
}
