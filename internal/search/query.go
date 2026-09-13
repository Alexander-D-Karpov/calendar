package search

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const (
	TypeAny   = ""
	TypeEvent = "event"
	TypeTodo  = "todo"

	maxTerms    = 32
	maxTermLen  = 100
	maxQueryLen = 500
)

type Term struct {
	Text   string
	Phrase bool
	Negate bool
}

type Query struct {
	Raw      string
	Terms    []Term
	Type     string
	In       []string
	Is       []string
	Has      []string
	After    *time.Time
	Before   *time.Time
	Priority *Compare
	Warnings []string
}

type Compare struct {
	Op    string
	Value int
}

var (
	flags    = []string{"done", "open", "overdue", "recurring", "private"}
	features = []string{"time", "checks", "body", "location", "reminders"}
	types    = map[string]string{"event": TypeEvent, "events": TypeEvent, "todo": TypeTodo, "todos": TypeTodo, "any": TypeAny, "all": TypeAny}
)

func (q Query) Empty() bool {
	return len(q.Terms) == 0 && len(q.In) == 0 && len(q.Is) == 0 && len(q.Has) == 0 &&
		q.After == nil && q.Before == nil && q.Priority == nil && q.Type == TypeAny
}

func (q Query) Text() bool {
	for _, t := range q.Terms {
		if !t.Negate {
			return true
		}
	}
	return false
}

func (q Query) Wants(kind string) bool {
	return q.Type == TypeAny || q.Type == kind
}

func Parse(raw string, today time.Time) Query {
	q := Query{Raw: strings.TrimSpace(raw)}
	if len(q.Raw) > maxQueryLen {
		q.Raw = q.Raw[:maxQueryLen]
		q.warn("The query was cut to %d characters.", maxQueryLen)
	}
	for _, tok := range tokenize(q.Raw) {
		q.token(tok, today)
	}
	if len(q.Terms) > maxTerms {
		q.Terms = q.Terms[:maxTerms]
		q.warn("Only the first %d words were used.", maxTerms)
	}
	return q
}

func (q *Query) warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if !slices.Contains(q.Warnings, msg) {
		q.Warnings = append(q.Warnings, msg)
	}
}

type token struct {
	text   string
	phrase bool
	negate bool
}

func tokenize(s string) []token {
	var out []token
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
			continue
		}
		t := token{}
		if s[i] == '-' && i+1 < len(s) && s[i+1] != ' ' {
			t.negate, i = true, i+1
		}
		start := i
		var b strings.Builder
		for i < len(s) {
			if s[i] == '"' {
				t.phrase = true
				i++
				for i < len(s) && s[i] != '"' {
					b.WriteByte(s[i])
					i++
				}
				if i < len(s) {
					i++
				}
				continue
			}
			if s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' {
				break
			}
			b.WriteByte(s[i])
			i++
		}
		if i == start {
			i++
		}
		t.text = b.String()
		if strings.TrimSpace(t.text) != "" {
			out = append(out, t)
		}
	}
	return out
}

func (q *Query) token(t token, today time.Time) {
	key, value, isFilter := strings.Cut(t.text, ":")
	if t.phrase || !isFilter || value == "" {
		q.term(t)
		return
	}
	key = strings.ToLower(key)
	value = strings.ToLower(strings.TrimSpace(value))
	switch key {
	case "type":
		if kind, ok := types[value]; ok {
			q.Type = kind
			return
		}
		q.warn("%q is not a type, use type:event or type:todo.", value)
	case "in":
		q.In = append(q.In, value)
		return
	case "is":
		if slices.Contains(flags, value) {
			if !slices.Contains(q.Is, value) {
				q.Is = append(q.Is, value)
			}
			return
		}
		q.warn("%q is not something to filter on, try is:%s.", value, strings.Join(flags, ", is:"))
	case "has":
		if slices.Contains(features, value) {
			if !slices.Contains(q.Has, value) {
				q.Has = append(q.Has, value)
			}
			return
		}
		q.warn("%q cannot be filtered with has:, try has:%s.", value, strings.Join(features, ", has:"))
	case "after", "before":
		if d, ok := parseWhen(value, today); ok {
			if key == "after" {
				q.After = &d
			} else {
				q.Before = &d
			}
			return
		}
		q.warn("%q is not a date, use %s:2026-09-10, %s:today or %s:+7d.", value, key, key, key)
	case "priority":
		if c, ok := parseCompare(value); ok {
			q.Priority = &c
			return
		}
		q.warn("%q is not a priority, use priority:2 or priority:>=2.", value)
	default:
		q.term(t)
		return
	}
	q.term(token{text: t.text, negate: t.negate})
}

func (q *Query) term(t token) {
	text := strings.TrimSpace(t.text)
	if text == "" {
		return
	}
	if len(text) > maxTermLen {
		text = strings.ToValidUTF8(text[:maxTermLen], "")
	}
	q.Terms = append(q.Terms, Term{Text: text, Phrase: t.phrase, Negate: t.negate})
}

func parseCompare(v string) (Compare, bool) {
	op := "="
	for _, prefix := range []string{">=", "<=", ">", "<", "="} {
		if rest, ok := strings.CutPrefix(v, prefix); ok {
			op, v = prefix, rest
			break
		}
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 || n > domain.MaxTodoPriority {
		return Compare{}, false
	}
	return Compare{Op: op, Value: n}, true
}

func parseWhen(v string, today time.Time) (time.Time, bool) {
	switch v {
	case "today":
		return today, true
	case "tomorrow":
		return today.AddDate(0, 0, 1), true
	case "yesterday":
		return today.AddDate(0, 0, -1), true
	}
	if d, err := time.Parse(domain.DateLayout, v); err == nil {
		return d, true
	}
	if d, ok := parseOffset(v, today); ok {
		return d, true
	}
	return time.Time{}, false
}

func parseOffset(v string, today time.Time) (time.Time, bool) {
	sign := 1
	switch {
	case strings.HasPrefix(v, "+"):
		v = v[1:]
	case strings.HasPrefix(v, "-"):
		sign, v = -1, v[1:]
	default:
		return time.Time{}, false
	}
	if len(v) < 2 {
		return time.Time{}, false
	}
	n, err := strconv.Atoi(v[:len(v)-1])
	if err != nil || n < 0 || n > 3650 {
		return time.Time{}, false
	}
	n *= sign
	switch v[len(v)-1] {
	case 'd':
		return today.AddDate(0, 0, n), true
	case 'w':
		return today.AddDate(0, 0, 7*n), true
	case 'm':
		return today.AddDate(0, n, 0), true
	case 'y':
		return today.AddDate(n, 0, 0), true
	}
	return time.Time{}, false
}

func (q Query) TSQuery() string {
	var parts []string
	for _, t := range q.Terms {
		word := normalize(t.Text)
		if word == "" {
			continue
		}
		if t.Negate {
			parts = append(parts, "-"+quote(word, t.Phrase))
			continue
		}
		parts = append(parts, quote(word, t.Phrase))
	}
	return strings.Join(parts, " ")
}

func quote(s string, phrase bool) string {
	if phrase || strings.ContainsAny(s, " \t") {
		return `"` + s + `"`
	}
	return s
}

func normalize(s string) string {
	s = strings.Map(func(r rune) rune {
		switch r {
		case '"', '\'', '\\', '(', ')', '|', '&', '!', '<', '>', ':':
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

func (q Query) Prefix() string {
	for i := len(q.Terms) - 1; i >= 0; i-- {
		t := q.Terms[i]
		if !t.Negate && !t.Phrase {
			return normalize(t.Text)
		}
	}
	return ""
}

var (
	todoOnlyFlags   = []string{"done", "open", "overdue"}
	eventOnlyFlags  = []string{"private"}
	todoOnlyFeature = []string{"checks"}
	eventOnlyFeat   = []string{"location"}
)

// Blocks reports the filter that makes this kind impossible, if any. A filter
// that cannot apply to a kind removes that kind rather than matching nothing.
func (q Query) Blocks(kind string) string {
	flags, features := eventOnlyFlags, eventOnlyFeat
	if kind == TypeEvent {
		flags, features = todoOnlyFlags, todoOnlyFeature
	}
	for _, f := range q.Is {
		if slices.Contains(flags, f) {
			return "is:" + f
		}
	}
	for _, f := range q.Has {
		if slices.Contains(features, f) {
			return "has:" + f
		}
	}
	if kind == TypeEvent && q.Priority != nil {
		return "priority:"
	}
	return ""
}

// Excludes reports whether the query removes anything with a -term.
func (q Query) Excludes() bool {
	for _, t := range q.Terms {
		if t.Negate && normalize(t.Text) != "" {
			return true
		}
	}
	return false
}
