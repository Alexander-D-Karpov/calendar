//go:build integration

package gsync_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/google"
	"github.com/Alexander-D-Karpov/calendar/internal/gsync"
	"github.com/Alexander-D-Karpov/calendar/internal/jobs"
	"github.com/Alexander-D-Karpov/calendar/internal/service"
	"github.com/Alexander-D-Karpov/calendar/internal/store/pg"
	"github.com/Alexander-D-Karpov/calendar/internal/testutil/pgtest"
)

type fakeRemote struct {
	mu     sync.Mutex
	n      int
	events map[string]google.Event
	marks  map[string]int
	tasks  map[string]google.Task
}

func newFakeRemote() *fakeRemote {
	return &fakeRemote{events: map[string]google.Event{}, marks: map[string]int{}, tasks: map[string]google.Task{}}
}

func (f *fakeRemote) tag() string {
	f.n++
	return `"` + strconv.Itoa(f.n) + `"`
}

func (f *fakeRemote) putEvent(e google.Event) google.Event {
	e.ETag = f.tag()
	if e.Status == "" {
		e.Status = google.StatusConfirmed
	}
	if e.Updated.IsZero() {
		e.Updated = time.Now().UTC()
	}
	f.events[e.ID], f.marks[e.ID] = e, f.n
	return e
}

func (f *fakeRemote) putTask(t google.Task) google.Task {
	t.ETag, t.Updated = f.tag(), time.Now().UTC()
	f.tasks[t.ID] = t
	return t
}

func (f *fakeRemote) edit(id string, fn func(*google.Event)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e := f.events[id]
	e.Updated = time.Now().UTC()
	fn(&e)
	f.putEvent(e)
}

func (f *fakeRemote) event(id string) google.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.events[id]
}

func (f *fakeRemote) task(id string) google.Task {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tasks[id]
}

func (f *fakeRemote) Calendars(context.Context) ([]google.Calendar, error) {
	return []google.Calendar{{ID: "cal", Name: "Work", AccessRole: "owner"}}, nil
}

func (f *fakeRemote) TaskLists(context.Context) ([]google.TaskList, error) {
	return []google.TaskList{{ID: "list", Name: "Inbox"}}, nil
}

func (f *fakeRemote) Events(_ context.Context, _, token string) (google.EventDelta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	since, _ := strconv.Atoi(token)
	d := google.EventDelta{SyncToken: strconv.Itoa(f.n)}
	for id, e := range f.events {
		if f.marks[id] > since {
			d.Items = append(d.Items, e)
		}
	}
	return d, nil
}

func (f *fakeRemote) Event(_ context.Context, _, id string) (google.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.events[id]
	if !ok {
		return e, google.ErrNotFound
	}
	return e, nil
}

func (f *fakeRemote) Instance(context.Context, string, string, string) (google.Event, error) {
	return google.Event{}, google.ErrNotFound
}

func (f *fakeRemote) InsertEvent(_ context.Context, _ string, e google.Event) (google.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e.ID, e.Editable, e.Updated = fmt.Sprintf("ev%d", f.n+1), true, time.Time{}
	return f.putEvent(e), nil
}

func (f *fakeRemote) PatchEvent(_ context.Context, _ string, e google.Event, ifMatch string) (google.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.events[e.ID]
	if !ok {
		return cur, google.ErrNotFound
	}
	if ifMatch != "" && ifMatch != cur.ETag {
		return cur, google.ErrPrecondition
	}
	cur.Summary, cur.Description, cur.Location, cur.Start, cur.End, cur.Status, cur.Reminders =
		e.Summary, e.Description, e.Location, e.Start, e.End, e.Status, e.Reminders
	if e.SendRecurrence {
		cur.Recurrence = e.Recurrence
	}
	cur.Updated = time.Now().UTC()
	return f.putEvent(cur), nil
}

func (f *fakeRemote) DeleteEvent(_ context.Context, _, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.events[id]
	if !ok {
		return google.ErrNotFound
	}
	cur.Status, cur.Updated = google.StatusCancelled, time.Now().UTC()
	f.putEvent(cur)
	return nil
}

func (f *fakeRemote) Watch(context.Context, string, string, string, string) (string, time.Time, error) {
	return "res", time.Now().Add(7 * 24 * time.Hour), nil
}

func (f *fakeRemote) StopWatch(context.Context, string, string) error {
	return nil
}

func (f *fakeRemote) Tasks(_ context.Context, _ string, since *time.Time) ([]google.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []google.Task
	for _, t := range f.tasks {
		if since == nil || !t.Updated.Before(*since) {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *fakeRemote) Task(_ context.Context, _, id string) (google.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[id]
	if !ok {
		return t, google.ErrNotFound
	}
	return t, nil
}

func (f *fakeRemote) InsertTask(_ context.Context, _ string, t google.Task) (google.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t.ID = fmt.Sprintf("t%d", f.n+1)
	return f.putTask(t), nil
}

func (f *fakeRemote) PatchTask(_ context.Context, _ string, t google.Task) (google.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.tasks[t.ID]
	if !ok {
		return cur, google.ErrNotFound
	}
	cur.Title, cur.Notes, cur.Status, cur.Due, cur.Completed = t.Title, t.Notes, t.Status, t.Due, t.Completed
	return f.putTask(cur), nil
}

func (f *fakeRemote) MoveTask(_ context.Context, _, id, parent string) (google.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.tasks[id]
	if !ok {
		return cur, google.ErrNotFound
	}
	cur.Parent = parent
	return f.putTask(cur), nil
}

func (f *fakeRemote) DeleteTask(_ context.Context, _, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.tasks[id]
	if !ok {
		return google.ErrNotFound
	}
	cur.Deleted = true
	f.putTask(cur)
	return nil
}

func TestGoogleSync(t *testing.T) {
	ctx := context.Background()
	st := pg.New(pgtest.New(t).Pool)
	keys, err := crypto.NewKeyRing(map[uint32][]byte{1: bytes.Repeat([]byte{9}, crypto.KeySize)}, 0)
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.CreateUser(ctx, domain.NewUser{
		ID: domain.NewID(), Email: "sync@example.com", Timezone: "Europe/Moscow",
		Identity: &domain.Identity{ID: domain.NewID(), Provider: domain.ProviderGoogle, Subject: "g1", Email: "sync@example.com", LinkedAt: time.Now()},
	})
	if err != nil {
		t.Fatal(err)
	}
	ids, err := st.ListIdentities(ctx, u.ID)
	if err != nil || len(ids) != 1 {
		t.Fatalf("identities = %v %v", ids, err)
	}
	cals, _ := st.ListCalendars(ctx, u.ID)
	lists, _ := st.ListTodoLists(ctx, u.ID)

	fake := newFakeRemote()
	fake.putEvent(google.Event{
		ID: "std", ICalUID: "std@google.com", Summary: "Standup", Editable: true,
		Start: google.When{DateTime: "2026-09-11T10:00:00+03:00", TimeZone: "Europe/Moscow"},
		End:   google.When{DateTime: "2026-09-11T10:15:00+03:00", TimeZone: "Europe/Moscow"},
	})
	fake.putTask(google.Task{ID: "t0", Title: "GPU", Notes: "PSU\n\n---\n[x] Risers\n[ ] Paste", Status: google.TaskOpen, Due: "2026-09-12"})

	eng := gsync.New(gsync.Options{
		Store: st, Keys: keys, Logger: slog.New(slog.DiscardHandler),
		Dial:    func(context.Context, string) (google.Remote, error) { return fake, nil },
		Enqueue: func(context.Context, string, any, jobs.Options) error { return nil },
	})
	if err := eng.Connect(ctx, u.ID, ids[0], "refresh", google.SyncScopes); err != nil {
		t.Fatal(err)
	}
	err = eng.Apply(ctx, u.ID, []gsync.Choice{
		{Entity: domain.BindCalendar, RemoteID: "cal", Target: cals[0].ID.String(), Direction: domain.SyncBoth},
		{Entity: domain.BindTaskList, RemoteID: "list", Target: lists[0].ID.String(), Direction: domain.SyncBoth},
	})
	if err != nil {
		t.Fatal(err)
	}
	bs, err := st.Bindings(ctx, u.ID)
	if err != nil || len(bs) != 2 {
		t.Fatalf("bindings = %v %v", bs, err)
	}
	byEntity := map[string]domain.SyncBinding{}
	for _, b := range bs {
		byEntity[b.Entity] = b
	}
	run := func(h func(context.Context, jobs.Job) error, entity string) {
		t.Helper()
		payload, _ := json.Marshal(gsync.BindingJob{Binding: byEntity[entity].ID})
		if err := h(ctx, jobs.Job{Payload: payload}); err != nil {
			t.Fatalf("%s: %v", entity, err)
		}
	}
	count := func(sql string) int {
		t.Helper()
		var n int
		if err := st.Pool().QueryRow(ctx, sql, u.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	const conflicts = `SELECT count(*) FROM sync_conflicts WHERE owner_id = $1`
	const outbox = `SELECT count(*) FROM sync_outbox o JOIN sync_bindings b ON b.id = o.binding_id WHERE b.owner_id = $1`
	events := service.NewEvents(st, st, st, clock.New(), 5000)

	run(eng.HandlePull, domain.BindCalendar)
	occ, err := events.List(ctx, u.ID, service.EventQuery{From: "2026-09-10", To: "2026-09-12", Expand: true})
	if err != nil || len(occ) != 1 || occ[0].Event.Title != "Standup" || occ[0].Event.UID != "std@google.com" {
		t.Fatalf("pulled = %+v %v", occ, err)
	}
	id := occ[0].Event.ID

	if _, err := events.Update(ctx, u.ID, id, domain.EventPatch{Title: domain.Some("Daily")}, service.Edit{}); err != nil {
		t.Fatal(err)
	}
	run(eng.HandlePush, domain.BindCalendar)
	if got := fake.event("std").Summary; got != "Daily" {
		t.Fatalf("pushed summary = %q", got)
	}
	run(eng.HandlePull, domain.BindCalendar)
	if count(conflicts) != 0 || count(outbox) != 0 {
		t.Fatal("an echoed push must not conflict")
	}

	if _, err := events.Update(ctx, u.ID, id, domain.EventPatch{Title: domain.Some("Local")}, service.Edit{}); err != nil {
		t.Fatal(err)
	}
	fake.edit("std", func(e *google.Event) { e.Summary, e.Updated = "Remote", time.Now().Add(time.Hour) })
	run(eng.HandlePull, domain.BindCalendar)
	if e, err := events.Get(ctx, u.ID, id); err != nil || e.Title != "Remote" {
		t.Fatalf("newest wins = %q %v", e.Title, err)
	}
	if count(conflicts) != 1 || count(outbox) != 0 {
		t.Fatal("the conflict must be logged and the losing push dropped")
	}

	fake.edit("std", func(e *google.Event) { e.Status = google.StatusCancelled })
	run(eng.HandlePull, domain.BindCalendar)
	if _, err := events.Get(ctx, u.ID, id); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cancelled err = %v", err)
	}

	todos := service.NewTodos(st, st, clock.New())
	run(eng.HandlePull, domain.BindTaskList)
	page, _, err := todos.List(ctx, u.ID, service.TodoQuery{Status: "all"})
	if err != nil || len(page) != 1 {
		t.Fatalf("todos = %v %v", page, err)
	}
	td := page[0]
	if td.Title != "GPU" || td.Body != "PSU" || len(td.Checks) != 2 || !td.Checks[0].Done || td.DueDate == nil {
		t.Fatalf("todo = %+v", td)
	}
	if _, _, err := todos.Complete(ctx, u.ID, td.ID, ""); err != nil {
		t.Fatal(err)
	}
	run(eng.HandlePush, domain.BindTaskList)
	if got := fake.task("t0"); got.Status != google.TaskCompleted || got.Notes != "PSU\n\n---\n[x] Risers\n[ ] Paste" {
		t.Fatalf("pushed task = %+v", got)
	}
}
