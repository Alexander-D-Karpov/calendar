package importer

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/store"
	"github.com/Alexander-D-Karpov/calendar/internal/transfer"
)

type writer struct {
	tx     store.ImportTx
	owner  domain.ID
	now    time.Time
	userTZ string
	stats  *Stats
}

func (w *writer) calendar(ctx context.Context, c transfer.Calendar, t Target) error {
	id, err := domain.ParseID(t.Target)
	if err != nil {
		cal, err := w.tx.CreateCalendar(ctx, newCalendar(w.owner, c, t.Name))
		if err != nil {
			return err
		}
		id = cal.ID
	}
	for _, sr := range c.Series {
		if err := w.series(ctx, id, sr); err != nil {
			return err
		}
	}
	return nil
}

func newCalendar(owner domain.ID, c transfer.Calendar, name string) domain.Calendar {
	if name == "" {
		name = "Imported"
	}
	color, ok := domain.NormalizeColor(c.Color)
	if !ok {
		color = domain.DefaultCalendarColor
	}
	tz := ""
	if domain.ValidTimezone(c.Timezone) {
		tz = c.Timezone
	}
	reminders := slices.Clone(c.Reminders)
	if reminders == nil {
		reminders = []int{}
	}
	return domain.Calendar{
		ID: domain.NewID(), OwnerID: owner, Name: transfer.Clip(name, 100), Color: color, Timezone: tz,
		Description: transfer.Clip(c.Description, 2000), Kind: domain.CalendarLocal, Hidden: c.Hidden,
		Position: -1, DefaultReminders: reminders,
	}
}

func (w *writer) prepare(e *domain.Event, cal domain.ID) {
	e.OwnerID, e.CalendarID = w.owner, cal
	if e.TZ == "" && !e.AllDay {
		e.TZ = w.userTZ
	}
	if e.Reminders == nil {
		e.Reminders = []int{}
	}
	e.Title = transfer.Clip(e.Title, 1000)
	e.Body = transfer.Clip(e.Body, 100000)
	e.Location = transfer.Clip(e.Location, 1000)
	if len(e.URL) > 2048 {
		e.URL = ""
	}
	if len(e.RDate) > 1000 {
		e.RDate = e.RDate[:1000]
	}
	if len(e.ExDate) > 5000 {
		e.ExDate = e.ExDate[:5000]
	}
	if len(e.Reminders) > domain.MaxReminders {
		e.Reminders = e.Reminders[:domain.MaxReminders]
	}
	if !e.End.After(e.Start) {
		if e.AllDay {
			e.End = e.Start.AddDate(0, 0, 1)
		} else if e.End.Before(e.Start) {
			e.End = e.Start
		}
	}
	if e.End.Sub(e.Start) > domain.MaxEventDuration {
		e.End = e.Start.Add(domain.MaxEventDuration)
	}
}

func (w *writer) series(ctx context.Context, cal domain.ID, sr transfer.Series) error {
	master := sr.Master
	w.prepare(&master, cal)
	if master.UID == "" {
		master.ID = domain.NewID()
		master.UID = master.ID.String()
	}
	var existing domain.Event
	found := false
	if master.ID == domain.NilID {
		switch cur, err := w.tx.EventByUID(ctx, cal, master.UID); {
		case err == nil:
			existing, found = cur, true
		case !errors.Is(err, domain.ErrNotFound):
			return err
		}
	}
	var saved domain.Event
	var err error
	if found {
		master.ID, master.OwnerID, master.Version = existing.ID, existing.OwnerID, existing.Version
		master.SeriesID, master.RecurrenceID = nil, nil
		master.Sequence = max(master.Sequence, existing.Sequence+1)
		saved, err = w.tx.Events().Update(ctx, master)
		w.stats.Updated++
	} else {
		if master.ID == domain.NilID {
			master.ID = domain.NewID()
		}
		master.Version = 1
		saved, err = w.tx.Events().Insert(ctx, master)
		w.stats.New++
	}
	if err != nil {
		return skippable(err, w.stats)
	}
	for _, ov := range sr.Overrides {
		o := ov
		w.prepare(&o, cal)
		if o.RecurrenceID == nil {
			continue
		}
		sid, rid := saved.ID, *o.RecurrenceID
		o.SeriesID, o.RecurrenceID, o.UID = &sid, &rid, saved.UID
		o.RRule, o.RDate, o.ExDate = "", nil, nil
		cur, err := w.tx.Events().Override(ctx, saved.ID, rid)
		switch {
		case err == nil:
			o.ID, o.Version = cur.ID, cur.Version
			_, err = w.tx.Events().Update(ctx, o)
			w.stats.Updated++
		case errors.Is(err, domain.ErrNotFound):
			o.ID, o.Version = domain.NewID(), 1
			_, err = w.tx.Events().Insert(ctx, o)
			w.stats.New++
		}
		if err != nil {
			if err = skippable(err, w.stats); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *writer) list(ctx context.Context, l transfer.List, t Target) error {
	id, err := domain.ParseID(t.Target)
	if err != nil {
		name := t.Name
		if name == "" {
			name = "Imported"
		}
		color, ok := domain.NormalizeColor(l.Color)
		if !ok {
			color = domain.DefaultListColor
		}
		created, err := w.tx.CreateTodoList(ctx, domain.TodoList{
			ID: domain.NewID(), OwnerID: w.owner, Name: transfer.Clip(name, 100), Color: color, Position: -1,
		})
		if err != nil {
			return err
		}
		id = created.ID
	}
	for _, n := range l.Todos {
		if err := w.todo(ctx, id, n); err != nil {
			return err
		}
	}
	return nil
}

func (w *writer) todo(ctx context.Context, list domain.ID, n transfer.TodoNode) error {
	t := n.Todo
	w.prepareTodo(&t, list, nil)
	var existing domain.Todo
	found := false
	if t.UID != "" {
		switch cur, err := w.tx.TodoByUID(ctx, list, t.UID); {
		case err == nil:
			existing, found = cur, true
		case !errors.Is(err, domain.ErrNotFound):
			return err
		}
	}
	var saved domain.Todo
	var err error
	if found {
		t.ID, t.Version, t.Position = existing.ID, existing.Version, existing.Position
		saved, err = w.tx.Todos().Update(ctx, t)
		if err == nil {
			if saved.Checks, err = w.checks(ctx, saved, n.Todo.Checks); err != nil {
				return err
			}
		}
		w.stats.Updated++
	} else {
		if t.Position, err = w.position(ctx, list, nil, t.ID); err != nil {
			return err
		}
		saved, err = w.tx.Todos().Insert(ctx, t)
		w.stats.New++
	}
	if err != nil {
		return skippable(err, w.stats)
	}
	for _, sub := range n.Subtasks {
		s := sub.Todo
		w.prepareTodo(&s, list, &saved.ID)
		if s.Position, err = w.position(ctx, list, &saved.ID, s.ID); err != nil {
			return err
		}
		if _, err := w.tx.Todos().Insert(ctx, s); err != nil {
			if err = skippable(err, w.stats); err != nil {
				return err
			}
			continue
		}
		w.stats.New++
	}
	return nil
}

func (w *writer) prepareTodo(t *domain.Todo, list domain.ID, parent *domain.ID) {
	t.ID, t.OwnerID, t.ListID, t.ParentID, t.Version = domain.NewID(), w.owner, list, parent, 1
	t.Title = transfer.Clip(t.Title, 1000)
	if t.Title == "" {
		t.Title = "(no title)"
	}
	t.Body = transfer.Clip(t.Body, 100000)
	if t.TZ == "" {
		t.TZ = w.userTZ
	}
	if t.DueTime == nil {
		t.Duration = 0
	}
	if t.DueDate == nil {
		t.DueTime, t.Duration, t.RRule = nil, 0, ""
	}
	if t.Reminders == nil {
		t.Reminders = []int{}
	}
	if len(t.Reminders) > domain.MaxReminders {
		t.Reminders = t.Reminders[:domain.MaxReminders]
	}
	if parent != nil {
		t.Checks = nil
	}
	prev := ""
	for i := range t.Checks {
		t.Checks[i].ID, t.Checks[i].TodoID = domain.NewID(), t.ID
		t.Checks[i].Position = domain.KeyBetween(prev, "")
		prev = t.Checks[i].Position
		if t.Checks[i].Done && t.Checks[i].DoneAt == nil {
			at := w.now
			t.Checks[i].DoneAt = &at
		}
	}
}

func (w *writer) position(ctx context.Context, list domain.ID, parent *domain.ID, self domain.ID) (string, error) {
	last, err := w.tx.Todos().LastPosition(ctx, list, parent, self)
	if err != nil {
		return "", err
	}
	return domain.KeyBetween(last, ""), nil
}

func (w *writer) checks(ctx context.Context, t domain.Todo, want []domain.Check) ([]domain.Check, error) {
	for _, c := range t.Checks {
		if err := w.tx.Todos().DeleteCheck(ctx, t.ID, c.ID); err != nil {
			return nil, err
		}
	}
	out := make([]domain.Check, 0, len(want))
	prev := ""
	for _, c := range want {
		c.ID, c.TodoID, c.Position = domain.NewID(), t.ID, domain.KeyBetween(prev, "")
		prev = c.Position
		if c.Done && c.DoneAt == nil {
			at := w.now
			c.DoneAt = &at
		}
		saved, err := w.tx.Todos().InsertCheck(ctx, c)
		if err != nil {
			return nil, err
		}
		out = append(out, saved)
	}
	return out, nil
}

func skippable(err error, stats *Stats) error {
	if errors.Is(err, domain.ErrInvalid) || errors.Is(err, domain.ErrConflict) {
		stats.Invalid++
		return nil
	}
	return err
}
