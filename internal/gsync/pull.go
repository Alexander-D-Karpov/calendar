package gsync

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/google"
	"github.com/Alexander-D-Karpov/calendar/internal/jobs"
	"github.com/Alexander-D-Karpov/calendar/internal/store"
)

const noTitle = "(no title)"

type clash struct {
	entity   string
	localID  domain.ID
	remoteID string
	localAt  time.Time
	remoteAt time.Time
	local    any
	remote   any
}

func (e *Engine) HandlePull(ctx context.Context, j jobs.Job) error {
	var p BindingJob
	if err := j.Decode(&p); err != nil {
		return jobs.Permanent(err)
	}
	b, acct, ok, err := e.load(ctx, p.Binding)
	if err != nil || !ok {
		return err
	}
	if b.Pulls() {
		r, err := e.remote(ctx, acct)
		if err != nil {
			return err
		}
		pctx := domain.WithOrigin(ctx, domain.OriginGoogle)
		var n int
		if b.Entity == domain.BindTaskList {
			n, err = e.pullTasks(pctx, r, &b)
		} else {
			n, err = e.pullEvents(pctx, r, &b)
		}
		e.observe(b.Entity, "pull", n, err)
		if err != nil {
			err = e.failed(ctx, acct, err)
			b.LastError, b.NextPollAt = scrub(err), e.now().Add(e.poll)
			return errors.Join(err, e.store.SaveBindingState(ctx, b))
		}
	}
	now := e.now()
	b.LastSyncedAt, b.LastError, b.NextPollAt = &now, "", now.Add(e.pollAfter(b))
	if err := e.store.SaveBindingState(ctx, b); err != nil {
		return err
	}
	return e.queuePush(ctx, b)
}

func boolOrder(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

func byRemote(ctx context.Context, tx store.SyncTx, entity, remote string) (domain.SyncMapping, bool, error) {
	m, err := tx.MappingByRemote(ctx, entity, remote)
	if errors.Is(err, domain.ErrNotFound) {
		return m, false, nil
	}
	return m, err == nil, err
}

func byLocal(ctx context.Context, tx store.SyncTx, entity string, id domain.ID) (domain.SyncMapping, bool, error) {
	m, err := tx.Mapping(ctx, entity, id)
	if errors.Is(err, domain.ErrNotFound) {
		return m, false, nil
	}
	return m, err == nil, err
}

func reconcile(ctx context.Context, tx store.SyncTx, entity string, seen map[string]bool, remove func(context.Context, domain.ID) error) error {
	ms, err := tx.Mappings(ctx, entity)
	if err != nil {
		return err
	}
	for _, m := range ms {
		if !seen[m.RemoteID] {
			if err := remove(ctx, m.LocalID); err != nil {
				return err
			}
		}
	}
	return nil
}

func localWins(ctx context.Context, tx store.SyncTx, b domain.SyncBinding, c clash) (bool, error) {
	if !b.Pushes() {
		return false, nil
	}
	_, pending, err := tx.PendingOp(ctx, c.entity, c.localID)
	if err != nil || !pending {
		return false, err
	}
	winner := domain.WinnerRemote
	if c.localAt.After(c.remoteAt) {
		winner = domain.WinnerLocal
	}
	err = tx.Conflict(ctx, domain.SyncConflict{
		OwnerID: b.OwnerID, BindingID: b.ID, Entity: c.entity, LocalID: c.localID, RemoteID: c.remoteID,
		Winner: winner, Local: c.local, Remote: c.remote,
	})
	if err != nil {
		return false, err
	}
	if winner == domain.WinnerLocal {
		return true, nil
	}
	return false, tx.DropOutbox(ctx, c.entity, c.localID)
}

func (e *Engine) pullEvents(ctx context.Context, r google.Remote, b *domain.SyncBinding) (int, error) {
	full := b.SyncToken == ""
	d, err := r.Events(ctx, b.RemoteID, b.SyncToken)
	if errors.Is(err, google.ErrGone) {
		full = true
		d, err = r.Events(ctx, b.RemoteID, "")
	}
	if err != nil {
		return 0, err
	}
	slices.SortStableFunc(d.Items, func(a, c google.Event) int {
		return boolOrder(a.RecurringEventID != "", c.RecurringEventID != "")
	})
	err = e.store.WithSync(ctx, b.OwnerID, b.ID, func(tx store.SyncTx) error {
		p, err := e.eventPuller(ctx, tx, *b)
		if err != nil {
			return err
		}
		seen := make(map[string]bool, len(d.Items))
		for _, re := range d.Items {
			seen[re.ID] = true
			if err := p.apply(ctx, re); err != nil {
				return err
			}
		}
		if !full {
			return nil
		}
		return reconcile(ctx, tx, domain.EntityEvent, seen, p.removeLocal)
	})
	if err != nil {
		return 0, err
	}
	b.SyncToken = d.SyncToken
	return len(d.Items), nil
}

type eventPull struct {
	e      *Engine
	tx     store.SyncTx
	b      domain.SyncBinding
	cal    domain.Calendar
	userTZ string
}

func (e *Engine) eventPuller(ctx context.Context, tx store.SyncTx, b domain.SyncBinding) (*eventPull, error) {
	if b.CalendarID == nil {
		return nil, jobs.Permanent(errors.New("gsync: calendar binding without a calendar"))
	}
	cal, err := e.store.Calendar(ctx, b.OwnerID, *b.CalendarID)
	if err != nil {
		return nil, err
	}
	u, err := e.store.UserByID(ctx, b.OwnerID)
	if err != nil {
		return nil, err
	}
	return &eventPull{e: e, tx: tx, b: b, cal: cal, userTZ: u.Timezone}, nil
}

func (p *eventPull) convert(ctx context.Context, re google.Event, base domain.Event) (domain.Event, bool) {
	ev, ok := eventFromRemote(re, base, []string{re.Start.TimeZone, p.cal.Timezone, p.userTZ}, p.cal.DefaultReminders)
	if !ok {
		p.e.logger.LogAttrs(ctx, slog.LevelWarn, "skipped google event without a start", slog.String("binding", p.b.ID.String()))
		return ev, false
	}
	ev.ReadOnly = ev.ReadOnly || p.cal.ReadOnly
	return ev, true
}

func (p *eventPull) save(ctx context.Context, out domain.Event, re google.Event) error {
	return p.tx.SaveMapping(ctx, domain.SyncMapping{
		Entity: domain.EntityEvent, LocalID: out.ID, RemoteID: re.ID, RemoteETag: re.ETag,
		RemoteUpdated: timePtr(re.Updated), LocalVersion: out.Version,
	})
}

func (p *eventPull) apply(ctx context.Context, re google.Event) error {
	if re.RecurringEventID != "" {
		return p.applyInstance(ctx, re)
	}
	m, mapped, err := byRemote(ctx, p.tx, domain.EntityEvent, re.ID)
	switch {
	case err != nil:
		return err
	case re.Status == google.StatusCancelled:
		if !mapped {
			return nil
		}
		if err := p.removeLocal(ctx, m.LocalID); err != nil {
			return err
		}
		return p.tx.DeleteMappingsWithPrefix(ctx, domain.EntityEvent, re.ID+"_")
	case !mapped:
		return p.create(ctx, re)
	}
	return p.update(ctx, m, re, func() error { return p.create(ctx, re) })
}

func (p *eventPull) removeLocal(ctx context.Context, id domain.ID) error {
	ev, err := p.tx.Events().Get(ctx, id)
	switch {
	case err == nil:
		if err := p.tx.Events().Delete(ctx, ev, p.e.now()); err != nil {
			return err
		}
	case !errors.Is(err, domain.ErrNotFound):
		return err
	}
	if err := p.tx.DropOutbox(ctx, domain.EntityEvent, id); err != nil {
		return err
	}
	return p.tx.DeleteMapping(ctx, domain.EntityEvent, id)
}

func (p *eventPull) create(ctx context.Context, re google.Event) error {
	id := domain.NewID()
	ev, ok := p.convert(ctx, re, domain.Event{ID: id, OwnerID: p.b.OwnerID, CalendarID: p.cal.ID, UID: id.String(), Version: 1})
	if !ok {
		return nil
	}
	if uid := strings.TrimSpace(re.ICalUID); uid != "" && len(uid) <= 1000 {
		taken, err := p.tx.UIDTaken(ctx, p.cal.ID, uid)
		if err != nil {
			return err
		}
		if !taken {
			ev.UID = uid
		}
	}
	out, err := p.tx.Events().Insert(ctx, ev)
	if err != nil {
		return err
	}
	return p.save(ctx, out, re)
}

func (p *eventPull) update(ctx context.Context, m domain.SyncMapping, re google.Event, recreate func() error) error {
	local, err := p.tx.Events().Get(ctx, m.LocalID)
	if errors.Is(err, domain.ErrNotFound) {
		op, pending, err := p.tx.PendingOp(ctx, domain.EntityEvent, m.LocalID)
		if err != nil {
			return err
		}
		if pending && op == domain.SyncDelete {
			return nil
		}
		if err := p.tx.DeleteMapping(ctx, domain.EntityEvent, m.LocalID); err != nil {
			return err
		}
		return recreate()
	}
	if err != nil {
		return err
	}
	if m.RemoteETag != "" && m.RemoteETag == re.ETag {
		return nil
	}
	wins, err := localWins(ctx, p.tx, p.b, clash{
		entity: domain.EntityEvent, localID: local.ID, remoteID: re.ID,
		localAt: local.UpdatedAt, remoteAt: re.Updated, local: local, remote: re,
	})
	if err != nil || wins {
		return err
	}
	ev, ok := p.convert(ctx, re, local)
	if !ok {
		return nil
	}
	out, err := p.tx.Events().Update(ctx, ev)
	if err != nil {
		return err
	}
	return p.save(ctx, out, re)
}

func (p *eventPull) applyInstance(ctx context.Context, re google.Event) error {
	mm, ok, err := byRemote(ctx, p.tx, domain.EntityEvent, re.RecurringEventID)
	if err != nil || !ok {
		return err
	}
	master, err := p.tx.Events().Get(ctx, mm.LocalID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	rid, _, ok := remoteTime(re.OriginalStart)
	if !ok {
		return nil
	}
	m, mapped, err := byRemote(ctx, p.tx, domain.EntityEvent, re.ID)
	if err != nil {
		return err
	}
	if re.Status == google.StatusCancelled {
		if mapped {
			if err := p.removeLocal(ctx, m.LocalID); err != nil {
				return err
			}
		}
		return p.exclude(ctx, master, mm, rid)
	}
	if !mapped {
		ov, err := p.tx.Events().Override(ctx, master.ID, rid)
		switch {
		case err == nil:
			m, mapped = domain.SyncMapping{Entity: domain.EntityEvent, LocalID: ov.ID, RemoteID: re.ID}, true
		case !errors.Is(err, domain.ErrNotFound):
			return err
		}
	}
	if mapped {
		return p.update(ctx, m, re, func() error { return nil })
	}
	sid, at := master.ID, rid
	id := domain.NewID()
	ev, ok := p.convert(ctx, re, domain.Event{
		ID: id, OwnerID: p.b.OwnerID, CalendarID: master.CalendarID, UID: master.UID, SeriesID: &sid, RecurrenceID: &at, Version: 1,
	})
	if !ok {
		return nil
	}
	ev.ReadOnly = ev.ReadOnly || master.ReadOnly
	out, err := p.tx.Events().Insert(ctx, ev)
	if err != nil {
		return err
	}
	return p.save(ctx, out, re)
}

func (p *eventPull) exclude(ctx context.Context, master domain.Event, mm domain.SyncMapping, at time.Time) error {
	if slices.ContainsFunc(master.ExDate, at.Equal) || len(master.ExDate) >= 5000 {
		return nil
	}
	master.ExDate = sortTimes(append(slices.Clone(master.ExDate), at))
	master.Sequence++
	out, err := p.tx.Events().Update(ctx, master)
	if err != nil {
		return err
	}
	mm.LocalVersion = out.Version
	return p.tx.SaveMapping(ctx, mm)
}

func (e *Engine) pullTasks(ctx context.Context, r google.Remote, b *domain.SyncBinding) (int, error) {
	if b.ListID == nil {
		return 0, jobs.Permanent(errors.New("gsync: task list binding without a list"))
	}
	started := e.now()
	full := b.UpdatedMin == nil
	items, err := r.Tasks(ctx, b.RemoteID, b.UpdatedMin)
	if err != nil {
		return 0, err
	}
	slices.SortStableFunc(items, func(a, c google.Task) int { return boolOrder(a.Parent != "", c.Parent != "") })
	err = e.store.WithSync(ctx, b.OwnerID, b.ID, func(tx store.SyncTx) error {
		p := &taskPull{e: e, tx: tx, b: *b, list: *b.ListID}
		seen := make(map[string]bool, len(items))
		for _, rt := range items {
			seen[rt.ID] = true
			if err := p.apply(ctx, rt); err != nil {
				return err
			}
		}
		if !full {
			return nil
		}
		return reconcile(ctx, tx, domain.EntityTodo, seen, p.removeLocal)
	})
	if err != nil {
		return 0, err
	}
	next := b.UpdatedMin
	for _, rt := range items {
		if next == nil || rt.Updated.After(*next) {
			t := rt.Updated
			next = &t
		}
	}
	if next == nil {
		t := started.Add(-5 * time.Minute)
		next = &t
	}
	b.UpdatedMin = next
	return len(items), nil
}

type taskPull struct {
	e    *Engine
	tx   store.SyncTx
	b    domain.SyncBinding
	list domain.ID
}

func (p *taskPull) apply(ctx context.Context, rt google.Task) error {
	m, mapped, err := byRemote(ctx, p.tx, domain.EntityTodo, rt.ID)
	switch {
	case err != nil:
		return err
	case rt.Deleted:
		if !mapped {
			return nil
		}
		return p.removeLocal(ctx, m.LocalID)
	case !mapped:
		return p.create(ctx, rt)
	}
	return p.update(ctx, m, rt)
}

func (p *taskPull) removeLocal(ctx context.Context, id domain.ID) error {
	t, err := p.tx.Todos().Get(ctx, id)
	switch {
	case err == nil:
		if err := p.tx.Todos().Delete(ctx, t, p.e.now()); err != nil {
			return err
		}
	case !errors.Is(err, domain.ErrNotFound):
		return err
	}
	if err := p.tx.DropOutbox(ctx, domain.EntityTodo, id); err != nil {
		return err
	}
	return p.tx.DeleteMapping(ctx, domain.EntityTodo, id)
}

func (p *taskPull) parent(ctx context.Context, remote string) (*domain.ID, error) {
	if remote == "" {
		return nil, nil
	}
	m, ok, err := byRemote(ctx, p.tx, domain.EntityTodo, remote)
	if err != nil || !ok {
		return nil, err
	}
	t, err := p.tx.Todos().Get(ctx, m.LocalID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	if err != nil || t.ParentID != nil || t.ListID != p.list {
		return nil, err
	}
	return &t.ID, nil
}

func (p *taskPull) save(ctx context.Context, out domain.Todo, rt google.Task) error {
	return p.tx.SaveMapping(ctx, domain.SyncMapping{
		Entity: domain.EntityTodo, LocalID: out.ID, RemoteID: rt.ID, RemoteETag: rt.ETag,
		RemoteUpdated: timePtr(rt.Updated), LocalVersion: out.Version, RemoteDueDate: todoDue(rt.Due),
	})
}

func (p *taskPull) create(ctx context.Context, rt google.Task) error {
	parent, err := p.parent(ctx, rt.Parent)
	if err != nil {
		return err
	}
	now := p.e.now()
	t := domain.Todo{ID: domain.NewID(), OwnerID: p.b.OwnerID, ListID: p.list, ParentID: parent, Status: domain.TodoOpen, Version: 1}
	t.Checks = newChecks(t.ID, fillTodo(&t, rt, nil, now), now)
	last, err := p.tx.Todos().LastPosition(ctx, p.list, parent, t.ID)
	if err != nil {
		return err
	}
	t.Position = domain.KeyBetween(last, "")
	out, err := p.tx.Todos().Insert(ctx, t)
	if err != nil {
		return err
	}
	return p.save(ctx, out, rt)
}

func (p *taskPull) update(ctx context.Context, m domain.SyncMapping, rt google.Task) error {
	local, err := p.tx.Todos().Get(ctx, m.LocalID)
	if errors.Is(err, domain.ErrNotFound) {
		op, pending, err := p.tx.PendingOp(ctx, domain.EntityTodo, m.LocalID)
		if err != nil {
			return err
		}
		if pending && op == domain.SyncDelete {
			return nil
		}
		if err := p.tx.DeleteMapping(ctx, domain.EntityTodo, m.LocalID); err != nil {
			return err
		}
		return p.create(ctx, rt)
	}
	if err != nil {
		return err
	}
	if local.ListID != p.list || (m.RemoteETag != "" && m.RemoteETag == rt.ETag) {
		return nil
	}
	wins, err := localWins(ctx, p.tx, p.b, clash{
		entity: domain.EntityTodo, localID: local.ID, remoteID: rt.ID,
		localAt: local.UpdatedAt, remoteAt: rt.Updated, local: local, remote: rt,
	})
	if err != nil || wins {
		return err
	}
	now := p.e.now()
	checks := fillTodo(&local, rt, &m, now)
	parent, err := p.parent(ctx, rt.Parent)
	if err != nil {
		return err
	}
	if !sameParent(parent, local.ParentID) {
		movable := true
		if parent != nil {
			has, err := p.tx.Todos().HasChildren(ctx, local.ID)
			if err != nil {
				return err
			}
			movable = !has && *parent != local.ID
		}
		if movable {
			last, err := p.tx.Todos().LastPosition(ctx, p.list, parent, local.ID)
			if err != nil {
				return err
			}
			local.ParentID, local.Position = parent, domain.KeyBetween(last, "")
		}
	}
	out, err := p.tx.Todos().Update(ctx, local)
	if err != nil {
		return err
	}
	if !sameChecks(out.Checks, checks) {
		if out.Checks, err = replaceChecks(ctx, p.tx.Todos(), out, checks, now); err != nil {
			return err
		}
	}
	return p.save(ctx, out, rt)
}

func fillTodo(t *domain.Todo, rt google.Task, m *domain.SyncMapping, now time.Time) []google.Check {
	body, checks := google.SplitNotes(rt.Notes)
	t.Title = clip(strings.TrimSpace(rt.Title), 1000)
	if t.Title == "" {
		t.Title = noTitle
	}
	t.Body = clip(body, 100000)
	switch {
	case rt.Status == google.TaskCompleted && !t.Done():
		at := now
		if rt.Completed != nil {
			at = rt.Completed.UTC()
		}
		t.Status, t.CompletedAt = domain.TodoCompleted, &at
	case rt.Status != google.TaskCompleted:
		t.Status, t.CompletedAt = domain.TodoOpen, nil
	}
	due := todoDue(rt.Due)
	switch {
	case due == nil:
		t.DueDate, t.DueTime, t.Duration, t.RRule = nil, nil, 0, ""
	case m != nil && sameDate(due, m.RemoteDueDate):
	default:
		t.DueDate = due
	}
	return checks
}

func newChecks(todo domain.ID, in []google.Check, now time.Time) []domain.Check {
	out := make([]domain.Check, 0, min(len(in), domain.MaxChecks))
	prev := ""
	for _, c := range in {
		text := clip(strings.TrimSpace(c.Text), 500)
		if text == "" || len(out) == domain.MaxChecks {
			continue
		}
		ch := domain.Check{ID: domain.NewID(), TodoID: todo, Text: text, Done: c.Done, Position: domain.KeyBetween(prev, "")}
		if c.Done {
			at := now
			ch.DoneAt = &at
		}
		prev = ch.Position
		out = append(out, ch)
	}
	return out
}

func replaceChecks(ctx context.Context, tx store.TodoTx, t domain.Todo, want []google.Check, now time.Time) ([]domain.Check, error) {
	for _, c := range t.Checks {
		if err := tx.DeleteCheck(ctx, t.ID, c.ID); err != nil {
			return nil, err
		}
	}
	fresh := newChecks(t.ID, want, now)
	out := make([]domain.Check, 0, len(fresh))
	for _, c := range fresh {
		saved, err := tx.InsertCheck(ctx, c)
		if err != nil {
			return nil, err
		}
		out = append(out, saved)
	}
	return out, nil
}
