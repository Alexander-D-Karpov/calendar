package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/recurrence"
	"github.com/Alexander-D-Karpov/calendar/internal/store"
)

type EventRepo interface {
	Event(ctx context.Context, owner, id domain.ID) (domain.Event, error)
	EventsInRange(ctx context.Context, owner domain.ID, calendars []domain.ID, from, to time.Time) ([]domain.Event, error)
	OverridesOf(ctx context.Context, owner domain.ID, series []domain.ID) ([]domain.Event, error)
	WithEvents(ctx context.Context, owner domain.ID, fn func(store.EventTx) error) error
}

type UserRepo interface {
	UserByID(ctx context.Context, id domain.ID) (domain.User, error)
}

type EventQuery struct {
	From      string
	To        string
	Calendars []string
	Expand    bool
}

type Edit struct {
	Scope    string
	Instance string
	IfMatch  string
}

type Events struct {
	repo  EventRepo
	cals  CalendarRepo
	users UserRepo
	clock clock.Clock
	max   int
}

func NewEvents(repo EventRepo, cals CalendarRepo, users UserRepo, clk clock.Clock, maxInstances int) *Events {
	if clk == nil {
		clk = clock.New()
	}
	if maxInstances <= 0 {
		maxInstances = 5000
	}
	return &Events{repo: repo, cals: cals, users: users, clock: clk, max: maxInstances}
}

type window struct {
	from, to, dayFrom, dayTo time.Time
}

func newWindow(from, to time.Time, loc *time.Location) window {
	f, t := from.In(loc), to.In(loc).Add(-time.Nanosecond)
	return window{from: from, to: to, dayFrom: midnight(f), dayTo: midnight(t).AddDate(0, 0, 1)}
}

func midnight(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func (w window) bounds(allDay bool) (time.Time, time.Time) {
	if allDay {
		return w.dayFrom, w.dayTo
	}
	return w.from, w.to
}

func (w window) overlaps(e domain.Event) bool {
	f, t := w.bounds(e.AllDay)
	return recurrence.Overlaps(e.Start, e.End, f, t)
}

func (s *Events) List(ctx context.Context, owner domain.ID, q EventQuery) ([]domain.Occurrence, error) {
	_, loc, err := s.userTZ(ctx, owner)
	if err != nil {
		return nil, err
	}
	var v domain.ValidationError
	from, to := parseRange(&v, q.From, q.To, loc)
	cals, err := s.pickCalendars(ctx, owner, &v, q.Calendars)
	if err != nil {
		return nil, err
	}
	if err := v.Err(); err != nil {
		return nil, err
	}
	if len(cals) == 0 {
		return []domain.Occurrence{}, nil
	}
	rows, err := s.repo.EventsInRange(ctx, owner, cals, from, to)
	if err != nil {
		return nil, err
	}
	if !q.Expand {
		out := make([]domain.Occurrence, len(rows))
		for i, e := range rows {
			out[i] = domain.Occurrence{Event: e, Instance: e.RecurrenceID}
		}
		sortOccurrences(out)
		return out, nil
	}
	return s.occurrences(ctx, owner, rows, newWindow(from, to, loc))
}

func (s *Events) Get(ctx context.Context, owner, id domain.ID) (domain.Event, error) {
	return s.repo.Event(ctx, owner, id)
}

func (s *Events) Instances(ctx context.Context, owner, id domain.ID, from, to string) ([]domain.Occurrence, error) {
	e, err := s.repo.Event(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	_, loc, err := s.userTZ(ctx, owner)
	if err != nil {
		return nil, err
	}
	var v domain.ValidationError
	f, t := parseRange(&v, from, to, loc)
	if err := v.Err(); err != nil {
		return nil, err
	}
	return s.occurrences(ctx, owner, []domain.Event{e}, newWindow(f, t, loc))
}

func (s *Events) occurrences(ctx context.Context, owner domain.ID, rows []domain.Event, w window) ([]domain.Occurrence, error) {
	var masters []domain.Event
	out := []domain.Occurrence{}
	overrides := map[domain.ID]domain.Event{}
	for _, e := range rows {
		switch {
		case e.IsOverride():
			overrides[e.ID] = e
		case e.IsMaster():
			masters = append(masters, e)
		case e.Status != domain.StatusCancelled && w.overlaps(e):
			out = append(out, domain.Occurrence{Event: e})
		}
	}
	if len(masters) > 0 {
		ids := make([]domain.ID, len(masters))
		for i, m := range masters {
			ids[i] = m.ID
		}
		ovs, err := s.repo.OverridesOf(ctx, owner, ids)
		if err != nil {
			return nil, err
		}
		for _, o := range ovs {
			overrides[o.ID] = o
		}
	}
	taken := make(map[string]bool, len(overrides))
	for _, o := range overrides {
		rid := *o.RecurrenceID
		taken[occurrenceKey(*o.SeriesID, rid)] = true
		if o.Status != domain.StatusCancelled && w.overlaps(o) {
			out = append(out, domain.Occurrence{Event: o, Instance: &rid})
		}
	}
	for _, m := range masters {
		if m.Status == domain.StatusCancelled {
			continue
		}
		limit := s.max - len(out)
		if limit <= 0 {
			return nil, recurrence.ErrTooMany
		}
		ser, err := recurrence.FromEvent(m)
		if err != nil {
			continue
		}
		f, t := w.bounds(m.AllDay)
		starts, err := ser.Between(f, t, limit)
		if err != nil {
			return nil, err
		}
		for _, st := range starts {
			if taken[occurrenceKey(m.ID, st)] {
				continue
			}
			occ := m
			occ.Start, occ.End = st, st.Add(m.Duration())
			inst := st
			out = append(out, domain.Occurrence{Event: occ, Instance: &inst})
		}
	}
	if len(out) > s.max {
		return nil, recurrence.ErrTooMany
	}
	sortOccurrences(out)
	return out, nil
}

func occurrenceKey(series domain.ID, at time.Time) string {
	return fmt.Sprintf("%s/%d", series, at.UnixMicro())
}

func sortOccurrences(list []domain.Occurrence) {
	slices.SortFunc(list, func(a, b domain.Occurrence) int {
		if c := a.Event.Start.Compare(b.Event.Start); c != 0 {
			return c
		}
		return strings.Compare(a.Event.ID.String(), b.Event.ID.String())
	})
}

func (s *Events) Create(ctx context.Context, owner domain.ID, p domain.EventPatch) (domain.Event, error) {
	cals, def, err := s.calendarMap(ctx, owner)
	if err != nil {
		return domain.Event{}, err
	}
	cal, err := pickCalendar(cals, def, p.CalendarID)
	if err != nil {
		return domain.Event{}, err
	}
	tz, _, err := s.userTZ(ctx, owner)
	if err != nil {
		return domain.Event{}, err
	}
	e := domain.Event{
		ID:           domain.NewID(),
		OwnerID:      owner,
		CalendarID:   cal.ID,
		Status:       domain.StatusConfirmed,
		Transparency: domain.TransparencyOpaque,
		Visibility:   domain.VisibilityDefault,
		Reminders:    slices.Clone(cal.DefaultReminders),
		Version:      1,
	}
	e.UID = e.ID.String()
	fillTZ(&e, cals, tz)
	if err := applyEvent(&e, p, true); err != nil {
		return domain.Event{}, err
	}
	var out domain.Event
	err = s.repo.WithEvents(ctx, owner, func(tx store.EventTx) error {
		var err error
		out, err = tx.Insert(ctx, e)
		return err
	})
	return out, uidConflict(err)
}

func (s *Events) Update(ctx context.Context, owner, id domain.ID, p domain.EventPatch, ed Edit) (domain.Event, error) {
	cals, def, err := s.calendarMap(ctx, owner)
	if err != nil {
		return domain.Event{}, err
	}
	tz, _, err := s.userTZ(ctx, owner)
	if err != nil {
		return domain.Event{}, err
	}
	var out domain.Event
	err = s.repo.WithEvents(ctx, owner, func(tx store.EventTx) error {
		t, err := resolve(ctx, tx, cals, tz, id, ed)
		if err != nil {
			return err
		}
		var move *domain.Calendar
		if p.CalendarID.Set {
			c, err := pickCalendar(cals, def, p.CalendarID)
			if err != nil {
				return err
			}
			if c.ID != t.master.CalendarID {
				if t.scope == domain.ScopeThis && t.master.IsMaster() {
					var v domain.ValidationError
					v.Add("calendar_id", "can only change for the whole series or following occurrences")
					return v.Err()
				}
				move = &c
			}
		}
		switch {
		case t.scope == domain.ScopeThis && t.master.IsMaster():
			out, err = updateThis(ctx, tx, t, p)
		case t.scope == domain.ScopeFollowing && t.master.IsMaster() && t.inst.After(t.master.Start):
			out, err = updateFollowing(ctx, tx, t.master, *t.inst, p, move)
		default:
			out, err = updateAll(ctx, tx, t.master, p, move)
		}
		return err
	})
	return out, uidConflict(err)
}

func (s *Events) Delete(ctx context.Context, owner, id domain.ID, ed Edit) error {
	cals, _, err := s.calendarMap(ctx, owner)
	if err != nil {
		return err
	}
	now := s.clock.Now().UTC()
	return s.repo.WithEvents(ctx, owner, func(tx store.EventTx) error {
		t, err := resolve(ctx, tx, cals, "UTC", id, ed)
		if err != nil {
			return err
		}
		m := t.master
		switch {
		case !m.IsMaster() || t.scope == domain.ScopeAll:
			return tx.Delete(ctx, m, now)
		case t.scope == domain.ScopeFollowing && !t.inst.After(m.Start):
			return tx.Delete(ctx, m, now)
		case t.scope == domain.ScopeFollowing:
			return truncateSeries(ctx, tx, m, *t.inst, now)
		default:
			return excludeInstance(ctx, tx, m, *t.inst, now)
		}
	})
}

type eventTarget struct {
	target domain.Event
	master domain.Event
	inst   *time.Time
	scope  string
}

func resolve(ctx context.Context, tx store.EventTx, cals map[domain.ID]domain.Calendar, tz string, id domain.ID, ed Edit) (eventTarget, error) {
	var v domain.ValidationError
	switch ed.Scope {
	case "", domain.ScopeThis, domain.ScopeFollowing, domain.ScopeAll:
	default:
		v.Add("scope", "must be this, following or all")
		return eventTarget{}, v.Err()
	}
	e, err := tx.Get(ctx, id)
	if err != nil {
		return eventTarget{}, err
	}
	if err := domain.CheckIfMatch(ed.IfMatch, e.ETag()); err != nil {
		return eventTarget{}, err
	}
	if err := writable(cals, e); err != nil {
		return eventTarget{}, err
	}
	fillTZ(&e, cals, tz)
	t := eventTarget{target: e, master: e, scope: ed.Scope}
	switch {
	case e.IsOverride():
		m, err := tx.Get(ctx, *e.SeriesID)
		if err != nil {
			return eventTarget{}, err
		}
		fillTZ(&m, cals, tz)
		rid := *e.RecurrenceID
		t.master, t.inst = m, &rid
		if t.scope == "" {
			t.scope = domain.ScopeThis
		}
	case e.IsMaster() && ed.Instance != "":
		inst, ok := parseEventTime(ed.Instance, e.AllDay, e.Zone())
		if !ok {
			v.Add("instance", timeFormatHint(e.AllDay))
			return eventTarget{}, v.Err()
		}
		ser, err := recurrence.FromEvent(e)
		if err != nil {
			return eventTarget{}, err
		}
		found, err := ser.Contains(inst)
		if err != nil {
			return eventTarget{}, err
		}
		if !found {
			return eventTarget{}, fmt.Errorf("%w: the series has no occurrence at %s", domain.ErrNotFound, ed.Instance)
		}
		t.inst = &inst
		if t.scope == "" {
			t.scope = domain.ScopeThis
		}
	case e.IsMaster() && (t.scope == domain.ScopeThis || t.scope == domain.ScopeFollowing):
		v.Add("instance", "is required for this scope")
		return eventTarget{}, v.Err()
	default:
		t.scope = domain.ScopeAll
	}
	return t, nil
}

func updateThis(ctx context.Context, tx store.EventTx, t eventTarget, p domain.EventPatch) (domain.Event, error) {
	ov := t.target
	isNew := false
	if !ov.IsOverride() {
		existing, err := tx.Override(ctx, t.master.ID, *t.inst)
		switch {
		case err == nil:
			ov = existing
			ov.TZ = t.master.TZ
		case errors.Is(err, domain.ErrNotFound):
			ov, isNew = newOverride(t.master, *t.inst), true
		default:
			return domain.Event{}, err
		}
	}
	if err := applyEvent(&ov, p, false); err != nil {
		return domain.Event{}, err
	}
	if isNew {
		return tx.Insert(ctx, ov)
	}
	return tx.Update(ctx, ov)
}

func updateAll(ctx context.Context, tx store.EventTx, m domain.Event, p domain.EventPatch, move *domain.Calendar) (domain.Event, error) {
	before := m
	if err := applyEvent(&m, p, false); err != nil {
		return domain.Event{}, err
	}
	shiftSeries(before, &m, p)
	if move != nil {
		m.CalendarID = move.ID
	}
	out, err := tx.Update(ctx, m)
	if err != nil {
		return domain.Event{}, err
	}
	if before.IsMaster() {
		if err := syncOverrides(ctx, tx, before, out); err != nil {
			return domain.Event{}, err
		}
	}
	return out, nil
}

func updateFollowing(ctx context.Context, tx store.EventTx, m domain.Event, at time.Time, p domain.EventPatch, move *domain.Calendar) (domain.Event, error) {
	head, tail, err := recurrence.Split(m, at)
	if err != nil {
		return domain.Event{}, err
	}
	headR, tailR := splitTimes(m.RDate, at)
	headX, tailX := splitTimes(m.ExDate, at)

	next := m
	next.ID = domain.NewID()
	next.UID = splitUID(m.UID, at)
	next.Start, next.End = at, at.Add(m.Duration())
	next.RRule, next.RDate, next.ExDate = tail, tailR, tailX
	next.Reminders = slices.Clone(m.Reminders)
	next.Sequence, next.Version = 0, 1
	base := next
	if err := applyEvent(&next, p, false); err != nil {
		return domain.Event{}, err
	}
	shiftSeries(base, &next, p)
	if move != nil {
		next.CalendarID = move.ID
	}

	m.RRule, m.RDate, m.ExDate = head, headR, headX
	m.Sequence++
	if _, err := tx.Update(ctx, m); err != nil {
		return domain.Event{}, err
	}
	created, err := tx.Insert(ctx, next)
	if err != nil {
		return domain.Event{}, err
	}
	if err := tx.MoveOverrides(ctx, m.ID, created, at); err != nil {
		return domain.Event{}, err
	}
	if err := syncOverrides(ctx, tx, base, created); err != nil {
		return domain.Event{}, err
	}
	return created, nil
}

func excludeInstance(ctx context.Context, tx store.EventTx, m domain.Event, at, now time.Time) error {
	if !slices.ContainsFunc(m.ExDate, at.Equal) {
		m.ExDate = append(slices.Clone(m.ExDate), at)
		slices.SortFunc(m.ExDate, func(a, b time.Time) int { return a.Compare(b) })
	}
	m.Sequence++
	if _, err := tx.Update(ctx, m); err != nil {
		return err
	}
	ov, err := tx.Override(ctx, m.ID, at)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return nil
	case err != nil:
		return err
	}
	return tx.Delete(ctx, ov, now)
}

func truncateSeries(ctx context.Context, tx store.EventTx, m domain.Event, at, now time.Time) error {
	head, _, err := recurrence.Split(m, at)
	if err != nil {
		return err
	}
	m.RRule = head
	m.RDate, _ = splitTimes(m.RDate, at)
	m.ExDate, _ = splitTimes(m.ExDate, at)
	m.Sequence++
	if _, err := tx.Update(ctx, m); err != nil {
		return err
	}
	ovs, err := tx.Overrides(ctx, m.ID)
	if err != nil {
		return err
	}
	for _, ov := range ovs {
		if !ov.RecurrenceID.Before(at) {
			if err := tx.Delete(ctx, ov, now); err != nil {
				return err
			}
		}
	}
	return nil
}

func syncOverrides(ctx context.Context, tx store.EventTx, before, after domain.Event) error {
	ovs, err := tx.Overrides(ctx, after.ID)
	if err != nil || len(ovs) == 0 {
		return err
	}
	if !after.IsMaster() {
		for _, ov := range ovs {
			if err := tx.Delete(ctx, ov, after.UpdatedAt); err != nil {
				return err
			}
		}
		return nil
	}
	ser, err := recurrence.FromEvent(after)
	if err != nil {
		return err
	}
	d := after.Start.Sub(before.Start)
	shift := d != 0 && before.AllDay == after.AllDay && before.RRule == after.RRule
	if shift && d > 0 {
		slices.Reverse(ovs)
	}
	for _, ov := range ovs {
		rid := *ov.RecurrenceID
		if shift {
			rid = rid.Add(d)
		}
		ok, err := ser.Contains(rid)
		if err != nil {
			return err
		}
		switch {
		case !ok:
			err = tx.Delete(ctx, ov, after.UpdatedAt)
		case shift:
			ov.RecurrenceID, ov.CalendarID = &rid, after.CalendarID
			_, err = tx.Update(ctx, ov)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func shiftSeries(before domain.Event, after *domain.Event, p domain.EventPatch) {
	d := after.Start.Sub(before.Start)
	if d == 0 || !before.IsMaster() || before.AllDay != after.AllDay || before.RRule != after.RRule {
		return
	}
	if !p.ExDate.Set {
		after.ExDate = shiftTimes(after.ExDate, d)
	}
	if !p.RDate.Set {
		after.RDate = shiftTimes(after.RDate, d)
	}
}

func newOverride(m domain.Event, inst time.Time) domain.Event {
	o := m
	sid, rid := m.ID, inst
	o.ID = domain.NewID()
	o.SeriesID, o.RecurrenceID = &sid, &rid
	o.Start, o.End = inst, inst.Add(m.Duration())
	o.RRule, o.RDate, o.ExDate = "", nil, nil
	o.Reminders = slices.Clone(m.Reminders)
	o.Version = 1
	return o
}

func splitTimes(ts []time.Time, at time.Time) ([]time.Time, []time.Time) {
	var before, after []time.Time
	for _, t := range ts {
		if t.Before(at) {
			before = append(before, t)
		} else {
			after = append(after, t)
		}
	}
	return before, after
}

func shiftTimes(ts []time.Time, d time.Duration) []time.Time {
	out := make([]time.Time, len(ts))
	for i, t := range ts {
		out[i] = t.Add(d)
	}
	return out
}

func splitUID(uid string, at time.Time) string {
	suffix := "_R" + at.UTC().Format("20060102T150405Z")
	if len(uid)+len(suffix) > 1000 {
		return domain.NewID().String()
	}
	return uid + suffix
}

func writable(cals map[domain.ID]domain.Calendar, e domain.Event) error {
	c, ok := cals[e.CalendarID]
	if !ok {
		return domain.ErrNotFound
	}
	if c.ReadOnly || e.ReadOnly {
		return fmt.Errorf("%w: event is read-only", domain.ErrForbidden)
	}
	return nil
}

func fillTZ(e *domain.Event, cals map[domain.ID]domain.Calendar, userTZ string) {
	if e.TZ != "" {
		return
	}
	if c := cals[e.CalendarID]; c.Timezone != "" {
		e.TZ = c.Timezone
		return
	}
	e.TZ = userTZ
}

func uidConflict(err error) error {
	if !errors.Is(err, domain.ErrConflict) {
		return err
	}
	var v domain.ValidationError
	v.Add("uid", "is already used by another event in this calendar")
	return v.Err()
}

func (s *Events) userTZ(ctx context.Context, owner domain.ID) (string, *time.Location, error) {
	return userZone(ctx, s.users, owner)
}

func (s *Events) calendarMap(ctx context.Context, owner domain.ID) (map[domain.ID]domain.Calendar, domain.Calendar, error) {
	list, err := s.cals.ListCalendars(ctx, owner)
	if err != nil {
		return nil, domain.Calendar{}, err
	}
	m, def := indexByID(list, func(c domain.Calendar) (domain.ID, bool) { return c.ID, c.IsDefault })
	return m, def, nil
}

func (s *Events) pickCalendars(ctx context.Context, owner domain.ID, v *domain.ValidationError, ids []string) ([]domain.ID, error) {
	cals, _, err := s.calendarMap(ctx, owner)
	if err != nil {
		return nil, err
	}
	var out []domain.ID
	if len(ids) == 0 {
		for id, c := range cals {
			if !c.Hidden {
				out = append(out, id)
			}
		}
		return out, nil
	}
	for _, raw := range ids {
		id, err := domain.ParseID(raw)
		if _, ok := cals[id]; err != nil || !ok {
			v.Addf("calendar_id", "unknown calendar %q", raw)
			continue
		}
		out = append(out, id)
	}
	return out, nil
}

func pickCalendar(cals map[domain.ID]domain.Calendar, def domain.Calendar, o domain.Opt[string]) (domain.Calendar, error) {
	var v domain.ValidationError
	c := def
	if raw := strings.TrimSpace(o.V); o.Set && !o.Null && raw != "" {
		id, err := domain.ParseID(raw)
		found := false
		if err == nil {
			c, found = cals[id]
		}
		if !found {
			v.Add("calendar_id", "unknown calendar")
			return domain.Calendar{}, v.Err()
		}
	}
	if c.ID == domain.NilID {
		return domain.Calendar{}, fmt.Errorf("%w: the account has no calendar", domain.ErrConflict)
	}
	if c.ReadOnly {
		v.Add("calendar_id", "calendar is read-only")
		return domain.Calendar{}, v.Err()
	}
	return c, nil
}
