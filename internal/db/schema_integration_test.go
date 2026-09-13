//go:build integration

package db_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Alexander-D-Karpov/calendar/internal/db"
	"github.com/Alexander-D-Karpov/calendar/internal/testutil/pgtest"
)

var (
	t0 = time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	t1 = time.Date(2026, 9, 10, 11, 0, 0, 0, time.UTC)
)

type fixture struct {
	t    *testing.T
	ctx  context.Context
	pool *pgxpool.Pool
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return &fixture{t: t, ctx: context.Background(), pool: pgtest.New(t).Pool}
}

func (f *fixture) id(sql string, args ...any) string {
	f.t.Helper()
	var id string
	if err := f.pool.QueryRow(f.ctx, sql, args...).Scan(&id); err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
	return id
}

func (f *fixture) exec(sql string, args ...any) error {
	_, err := f.pool.Exec(f.ctx, sql, args...)
	return err
}

func (f *fixture) mustExec(sql string, args ...any) {
	f.t.Helper()
	if err := f.exec(sql, args...); err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
}

func (f *fixture) scalar(dest any, sql string, args ...any) {
	f.t.Helper()
	if err := f.pool.QueryRow(f.ctx, sql, args...).Scan(dest); err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
}

func (f *fixture) user() string {
	return f.id(`INSERT INTO users (id, email)
		VALUES (gen_random_uuid(), gen_random_uuid()::text || '@example.com')
		RETURNING id::text`)
}

func (f *fixture) calendar(owner, name string) string {
	return f.id(`INSERT INTO calendars (id, owner_id, name)
		VALUES (gen_random_uuid(), $1, $2)
		RETURNING id::text`, owner, name)
}

const insertEventSQL = `INSERT INTO events (id, owner_id, calendar_id, ical_uid, title, start_at, end_at, tz, span)
	VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, 'UTC', tstzrange($5, $6))
	RETURNING id::text`

func (f *fixture) event(owner, cal, uid, title string) string {
	return f.id(insertEventSQL, owner, cal, uid, title, t0, t1)
}

func (f *fixture) list(owner string) string {
	return f.id(`INSERT INTO todo_lists (id, owner_id, name)
		VALUES (gen_random_uuid(), $1, 'Inbox')
		RETURNING id::text`, owner)
}

const insertTodoSQL = `INSERT INTO todos (id, owner_id, list_id, parent_id, title, position)
	VALUES (gen_random_uuid(), $1, $2, $3, $4, 'a0')
	RETURNING id::text`

func (f *fixture) todo(owner, list string, parent any, title string) string {
	return f.id(insertTodoSQL, owner, list, parent, title)
}

func TestEventCannotReferenceForeignCalendar(t *testing.T) {
	f := newFixture(t)
	alice, bob := f.user(), f.user()
	cal := f.calendar(alice, "Work")

	var id string
	err := f.pool.QueryRow(f.ctx, insertEventSQL, bob, cal, "uid-1", "x", t0, t1).Scan(&id)
	if !db.IsForeignKeyViolation(err) {
		t.Fatalf("err = %v, want foreign key violation", err)
	}
}

func TestEventURLMustBeHTTP(t *testing.T) {
	f := newFixture(t)
	alice := f.user()
	cal := f.calendar(alice, "Work")
	insert := `INSERT INTO events (id, owner_id, calendar_id, ical_uid, title, url, start_at, end_at, tz, span)
		VALUES (gen_random_uuid(), $1, $2, gen_random_uuid()::text, 'x', $3, $4, $5, 'UTC', tstzrange($4, $5))`

	for _, u := range []string{"", "https://example.com/a?b=c", "HTTP://EXAMPLE.COM"} {
		if err := f.exec(insert, alice, cal, u, t0, t1); err != nil {
			t.Errorf("url %q rejected: %v", u, err)
		}
	}
	for _, u := range []string{"javascript:alert(1)", "data:text/html,x", "//example.com", "https://exa mple.com", "ftp://example.com"} {
		if err := f.exec(insert, alice, cal, u, t0, t1); !db.IsCheckViolation(err, "events_url_check") {
			t.Errorf("url %q err = %v, want events_url_check", u, err)
		}
	}
}

func TestOverrideFollowsMasterCalendar(t *testing.T) {
	f := newFixture(t)
	alice := f.user()
	work, home := f.calendar(alice, "Work"), f.calendar(alice, "Home")
	master := f.event(alice, work, "series-uid", "Standup")
	override := f.id(`INSERT INTO events
		(id, owner_id, calendar_id, ical_uid, series_id, recurrence_id, title, start_at, end_at, tz, span)
		VALUES (gen_random_uuid(), $1, $2, 'series-uid', $3, $4, 'Standup moved', $4, $5, 'UTC', tstzrange($4, $5))
		RETURNING id::text`, alice, work, master, t0, t1)

	f.mustExec(`UPDATE events SET calendar_id = $1 WHERE id = $2`, home, master)

	var got string
	f.scalar(&got, `SELECT calendar_id::text FROM events WHERE id = $1`, override)
	if got != home {
		t.Fatalf("override calendar = %s, want %s", got, home)
	}

	f.mustExec(`DELETE FROM events WHERE id = $1`, master)
	var n int
	f.scalar(&n, `SELECT count(*) FROM events WHERE id = $1`, override)
	if n != 0 {
		t.Fatal("override must be deleted with its master")
	}
}

func TestTodoDepthLimited(t *testing.T) {
	f := newFixture(t)
	alice := f.user()
	list := f.list(alice)
	parent := f.todo(alice, list, nil, "parent")
	child := f.todo(alice, list, parent, "child")

	var id string
	err := f.pool.QueryRow(f.ctx, insertTodoSQL, alice, list, child, "grandchild").Scan(&id)
	if !db.IsCheckViolation(err, "todos_depth_check") {
		t.Fatalf("grandchild err = %v, want todos_depth_check", err)
	}

	other := f.todo(alice, list, nil, "other")
	err = f.exec(`UPDATE todos SET parent_id = $1 WHERE id = $2`, other, parent)
	if !db.IsCheckViolation(err, "todos_depth_check") {
		t.Fatalf("reparent err = %v, want todos_depth_check", err)
	}
}

func TestTodoDepthIgnoresDeletedChildren(t *testing.T) {
	f := newFixture(t)
	alice := f.user()
	list := f.list(alice)
	parent := f.todo(alice, list, nil, "parent")
	child := f.todo(alice, list, parent, "child")
	other := f.todo(alice, list, nil, "other")

	f.mustExec(`UPDATE todos SET deleted_at = now() WHERE id = $1`, child)
	f.mustExec(`UPDATE todos SET parent_id = $1 WHERE id = $2`, other, parent)

	err := f.exec(`UPDATE todos SET deleted_at = NULL WHERE id = $1`, child)
	if !db.IsCheckViolation(err, "todos_depth_check") {
		t.Fatalf("restore err = %v, want todos_depth_check", err)
	}
}

func TestTodoConcurrentReparentCannotCycle(t *testing.T) {
	f := newFixture(t)
	alice := f.user()
	list := f.list(alice)
	a := f.todo(alice, list, nil, "a")
	b := f.todo(alice, list, nil, "b")

	tx1, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx1.Rollback(f.ctx)
	if _, err := tx1.Exec(f.ctx, `UPDATE todos SET parent_id = $1 WHERE id = $2`, b, a); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		tx2, err := f.pool.Begin(f.ctx)
		if err != nil {
			done <- err
			return
		}
		if _, err := tx2.Exec(f.ctx, `UPDATE todos SET parent_id = $1 WHERE id = $2`, a, b); err != nil {
			_ = tx2.Rollback(f.ctx)
			done <- err
			return
		}
		done <- tx2.Commit(f.ctx)
	}()

	time.Sleep(200 * time.Millisecond)
	if err := tx1.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !db.IsCheckViolation(err, "todos_depth_check") {
		t.Fatalf("concurrent reparent err = %v, want todos_depth_check", err)
	}
}

func TestSubtasksMoveWithParent(t *testing.T) {
	f := newFixture(t)
	alice := f.user()
	inbox, work := f.list(alice), f.list(alice)
	parent := f.todo(alice, inbox, nil, "parent")
	child := f.todo(alice, inbox, parent, "child")

	if err := f.exec(`UPDATE todos SET list_id = $1 WHERE id = $2`, work, child); !db.IsForeignKeyViolation(err) {
		t.Fatalf("moving subtask alone err = %v, want foreign key violation", err)
	}

	f.mustExec(`UPDATE todos SET list_id = $1 WHERE id = $2`, work, parent)
	var got string
	f.scalar(&got, `SELECT list_id::text FROM todos WHERE id = $1`, child)
	if got != work {
		t.Fatalf("child list = %s, want %s", got, work)
	}
}

func TestTodoSearchIncludesChecks(t *testing.T) {
	f := newFixture(t)
	alice := f.user()
	list := f.list(alice)
	todo := f.todo(alice, list, nil, "Assemble GPU node")

	matches := func(q string) bool {
		t.Helper()
		var ok bool
		f.scalar(&ok, `SELECT search @@ websearch_to_tsquery('simple', f_unaccent($2)) FROM todos WHERE id = $1`, todo, q)
		return ok
	}

	if !matches("gpu") {
		t.Fatal("title must be searchable")
	}
	check := f.id(`INSERT INTO todo_checks (id, todo_id, text, position)
		VALUES (gen_random_uuid(), $1, 'Thermal paste', 'a0')
		RETURNING id::text`, todo)
	if !matches("thermal") {
		t.Fatal("check text must be searchable after insert")
	}
	f.mustExec(`UPDATE todo_checks SET text = 'Riser cables' WHERE id = $1`, check)
	if matches("thermal") || !matches("riser") {
		t.Fatal("check text must be reindexed after update")
	}
	f.mustExec(`DELETE FROM todo_checks WHERE id = $1`, check)
	if matches("riser") {
		t.Fatal("check text must be removed after delete")
	}
}

func TestEventSearchUnaccentAndStemming(t *testing.T) {
	f := newFixture(t)
	alice := f.user()
	cal := f.calendar(alice, "Work")
	ev := f.event(alice, cal, "uid-cafe", "Café meeting")

	cases := map[string]string{
		"cafe":     "simple",
		"meetings": "english",
	}
	for q, cfg := range cases {
		var ok bool
		f.scalar(&ok, `SELECT search @@ websearch_to_tsquery($2::regconfig, f_unaccent($3)) FROM events WHERE id = $1`, ev, cfg, q)
		if !ok {
			t.Errorf("query %q (%s) did not match", q, cfg)
		}
	}
}

func TestChangesNotifyAndSequence(t *testing.T) {
	f := newFixture(t)
	alice := f.user()

	conn, err := f.pool.Acquire(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(f.ctx, "LISTEN calendar_changes"); err != nil {
		t.Fatal(err)
	}

	insert := `INSERT INTO changes (owner_id, entity, entity_id, op, span)
		VALUES ($1, 'event', gen_random_uuid(), 'create', tstzrange($2, $3))
		RETURNING seq`
	var seq1, seq2 int64
	f.scalar(&seq1, insert, alice, t0, t1)
	f.scalar(&seq2, insert, alice, t0, t1)
	if seq2 <= seq1 {
		t.Fatalf("seq not increasing: %d then %d", seq1, seq2)
	}

	waitCtx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancel()
	n, err := conn.Conn().WaitForNotification(waitCtx)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Seq    int64  `json:"seq"`
		Owner  string `json:"owner"`
		Entity string `json:"entity"`
		Op     string `json:"op"`
		From   string `json:"from"`
	}
	if err := json.Unmarshal([]byte(n.Payload), &payload); err != nil {
		t.Fatalf("payload %q: %v", n.Payload, err)
	}
	if payload.Seq != seq1 || payload.Owner != alice || payload.Entity != "event" || payload.Op != "create" || payload.From == "" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestDuplicateCandidatesCleanedUp(t *testing.T) {
	f := newFixture(t)
	alice := f.user()
	cal := f.calendar(alice, "Work")
	a := f.event(alice, cal, "uid-a", "Standup")
	b := f.event(alice, cal, "uid-b", "Standup")

	f.mustExec(`INSERT INTO duplicate_candidates (id, owner_id, entity, a_id, b_id, reason)
		VALUES (gen_random_uuid(), $1, 'event', least($2::uuid, $3::uuid), greatest($2::uuid, $3::uuid), 'fingerprint')`,
		alice, a, b)
	f.mustExec(`DELETE FROM events WHERE id = $1`, a)

	var n int
	f.scalar(&n, `SELECT count(*) FROM duplicate_candidates WHERE owner_id = $1`, alice)
	if n != 0 {
		t.Fatalf("%d candidates left after deleting an event", n)
	}
}

func TestJobUniqueKey(t *testing.T) {
	f := newFixture(t)
	insert := `INSERT INTO jobs (kind, unique_key) VALUES ($1, 'binding-1')`

	f.mustExec(insert, "sync")
	if err := f.exec(insert, "sync"); !db.IsUniqueViolation(err, "jobs_unique_key_idx") {
		t.Fatalf("second queued err = %v, want jobs_unique_key_idx violation", err)
	}

	f.mustExec(insert, "refresh")

	f.mustExec(`UPDATE jobs SET status = 'running', locked_by = 'w1', locked_at = now() WHERE kind = 'sync'`)
	f.mustExec(insert, "sync")
	if err := f.exec(insert, "sync"); !db.IsUniqueViolation(err, "jobs_unique_key_idx") {
		t.Fatalf("queued while running err = %v, want jobs_unique_key_idx violation", err)
	}

	f.mustExec(`UPDATE jobs SET status = 'failed', locked_by = NULL, locked_at = NULL WHERE kind = 'sync' AND status = 'running'`)
	var queued int
	f.scalar(&queued, `SELECT count(*) FROM jobs WHERE kind = 'sync' AND status = 'queued'`)
	if queued != 1 {
		t.Fatalf("queued sync jobs = %d, want 1", queued)
	}
}
