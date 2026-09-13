package search

import (
	"strconv"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const (
	englishConfig = "english"
	simpleConfig  = "simple"
)

type builder struct {
	sql  strings.Builder
	args []any
}

func (b *builder) arg(v any) string {
	b.args = append(b.args, v)
	return "$" + strconv.Itoa(len(b.args))
}

func (b *builder) write(s string) {
	b.sql.WriteString(s)
}

type Params struct {
	Owner    domain.ID
	Query    Query
	Now      time.Time
	Today    time.Time
	Limit    int
	Offset   int
	Fallback bool
}

// eventSQL and todoSQL return one row per entry with the columns the scanner
// reads. They are combined with UNION ALL so ranking is shared, which keeps a
// mixed result list in one ordered page rather than two lists stitched together.
const selectHead = `SELECT kind, id, title, snippet, starts, ends, all_day, container, color, done, rank`

func Build(p Params) (string, []any) {
	b := &builder{}
	owner := b.arg(p.Owner)
	ts, hasText := tsArgs(b, p.Query)
	b.write("WITH results AS (\n")
	first := true
	if p.Query.Wants(TypeEvent) {
		buildEvents(b, p, owner, ts, hasText)
		first = false
	}
	if p.Query.Wants(TypeTodo) {
		if !first {
			b.write("\nUNION ALL\n")
		}
		buildTodos(b, p, owner, ts, hasText)
	}
	b.write("\n)\n" + selectHead + " FROM results ORDER BY rank DESC, starts DESC NULLS LAST, id DESC")
	b.write(" LIMIT " + b.arg(p.Limit) + " OFFSET " + b.arg(p.Offset))
	return b.sql.String(), b.args
}

type tsRefs struct {
	english string
	simple  string
	prefix  string
	fuzzy   bool
}

func tsArgs(b *builder, q Query) (tsRefs, bool) {
	text := q.TSQuery()
	if text == "" {
		return tsRefs{}, false
	}
	raw := b.arg(text)
	refs := tsRefs{
		english: "websearch_to_tsquery('" + englishConfig + "'::regconfig, f_unaccent(" + raw + "))",
		simple:  "websearch_to_tsquery('" + simpleConfig + "'::regconfig, f_unaccent(" + raw + "))",
	}
	// The prefix is matched on the title alone, beside the tsquery rather than
	// inside it, so with a -term it would OR back a row the exclusion removed.
	if pre := q.Prefix(); pre != "" && !q.Excludes() {
		refs.prefix = b.arg(strings.ToLower(pre))
		refs.fuzzy = true
	}
	return refs, true
}

func matchClause(refs tsRefs, titleCol string) string {
	m := "(search @@ " + refs.english + " OR search @@ " + refs.simple
	if refs.fuzzy {
		m += " OR lower(f_unaccent(" + titleCol + ")) LIKE " + refs.prefix + " || '%'"
	}
	return m + ")"
}

func rankExpr(refs tsRefs, titleCol string, hasText bool, recency string) string {
	if !hasText {
		return "(1 + " + recency + ")"
	}
	rank := "(ts_rank_cd(search, " + refs.english + ") + ts_rank_cd(search, " + refs.simple + ")"
	if refs.fuzzy {
		rank += " + similarity(lower(f_unaccent(" + titleCol + ")), " + refs.prefix + ") * 0.4"
	}
	return rank + " + " + recency + ")"
}

// recency nudges entries near today up without letting a far-future entry beat
// a strong text match: it decays to zero over about a year in either direction.
func recencyExpr(b *builder, now time.Time, col string) string {
	ref := b.arg(now)
	return "greatest(0.0, 0.3 - abs(extract(epoch FROM (" + col + " - " + ref + "))) / 100000000.0)"
}

func buildEvents(b *builder, p Params, owner string, refs tsRefs, hasText bool) {
	q := p.Query
	recency := recencyExpr(b, p.Now, "e.start_at")
	b.write("SELECT 'event'::text AS kind, e.id, e.title,")
	b.write(" left(regexp_replace(e.body, '\\s+', ' ', 'g'), 200) AS snippet,")
	b.write(" e.start_at AS starts, e.end_at AS ends, e.all_day, c.name AS container, c.color AS color,")
	b.write(" false AS done, " + rankExpr(refs, "e.title", hasText, recency) + "::float8 AS rank")
	b.write(" FROM events e JOIN calendars c ON c.id = e.calendar_id")
	b.write(" WHERE e.owner_id = " + owner + " AND e.deleted_at IS NULL AND e.series_id IS NULL")
	if hasText {
		b.write(" AND " + matchClause(refs, "e.title"))
	}
	if len(q.In) > 0 {
		b.write(" AND lower(c.name) = ANY(" + b.arg(q.In) + ")")
	}
	for _, flag := range q.Is {
		switch flag {
		case "recurring":
			b.write(" AND (e.rrule IS NOT NULL OR cardinality(e.rdate) > 0)")
		case "private":
			b.write(" AND e.visibility = 'private'")
		}
	}
	for _, feature := range q.Has {
		switch feature {
		case "body":
			b.write(" AND e.body <> ''")
		case "location":
			b.write(" AND e.location <> ''")
		case "time":
			b.write(" AND NOT e.all_day")
		case "reminders":
			b.write(" AND EXISTS (SELECT 1 FROM event_reminders r WHERE r.event_id = e.id)")
		}
	}
	if q.After != nil {
		b.write(" AND upper(e.span) > " + b.arg(*q.After))
	}
	if q.Before != nil {
		b.write(" AND lower(e.span) < " + b.arg(*q.Before))
	}
}

func buildTodos(b *builder, p Params, owner string, refs tsRefs, hasText bool) {
	q := p.Query
	recency := recencyExpr(b, p.Now, "t.due_date::timestamptz")
	b.write("SELECT 'todo'::text AS kind, t.id, t.title,")
	b.write(" left(regexp_replace(t.body, '\\s+', ' ', 'g'), 200) AS snippet,")
	b.write(" t.due_date::timestamptz AS starts, NULL::timestamptz AS ends, (t.due_time IS NULL) AS all_day,")
	b.write(" l.name AS container, l.color AS color, t.status = 'completed' AS done,")
	b.write(" " + rankExpr(refs, "t.title", hasText, recency) + "::float8 AS rank")
	b.write(" FROM todos t JOIN todo_lists l ON l.id = t.list_id")
	b.write(" WHERE t.owner_id = " + owner + " AND t.deleted_at IS NULL")
	if hasText {
		b.write(" AND " + matchClause(refs, "t.title"))
	}
	if len(q.In) > 0 {
		b.write(" AND lower(l.name) = ANY(" + b.arg(q.In) + ")")
	}
	for _, flag := range q.Is {
		switch flag {
		case "done":
			b.write(" AND t.status = 'completed'")
		case "open":
			b.write(" AND t.status = 'needs_action'")
		case "overdue":
			b.write(" AND t.status = 'needs_action' AND t.due_date IS NOT NULL AND t.due_date < " + b.arg(p.Today))
		case "recurring":
			b.write(" AND t.rrule IS NOT NULL")
		}
	}
	for _, feature := range q.Has {
		switch feature {
		case "body":
			b.write(" AND t.body <> ''")
		case "time":
			b.write(" AND t.due_time IS NOT NULL")
		case "checks":
			b.write(" AND EXISTS (SELECT 1 FROM todo_checks ch WHERE ch.todo_id = t.id)")
		case "reminders":
			b.write(" AND EXISTS (SELECT 1 FROM todo_reminders r WHERE r.todo_id = t.id)")
		}
	}
	if c := q.Priority; c != nil {
		b.write(" AND t.priority " + compareOp(c.Op) + " " + b.arg(int16(c.Value)))
	}
	if q.After != nil {
		b.write(" AND t.due_date >= " + b.arg(*q.After))
	}
	if q.Before != nil {
		b.write(" AND t.due_date < " + b.arg(*q.Before))
	}
}

func compareOp(op string) string {
	switch op {
	case ">", ">=", "<", "<=":
		return op
	}
	return "="
}
