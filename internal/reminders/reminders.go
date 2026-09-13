package reminders

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/jobs"
	"github.com/Alexander-D-Karpov/calendar/internal/metrics"
	"github.com/Alexander-D-Karpov/calendar/internal/push"
	"github.com/Alexander-D-Karpov/calendar/internal/recurrence"
	"github.com/Alexander-D-Karpov/calendar/internal/view"
)

const (
	lookback  = 5 * time.Minute
	maxItems  = 2000
	maxOccurs = 200
)

type Repo interface {
	UserByID(ctx context.Context, id domain.ID) (domain.User, error)
	PushSubscriptions(ctx context.Context, user domain.ID) ([]domain.PushSubscription, error)
	DropPushSubscription(ctx context.Context, id domain.ID) error
	TouchPushSubscription(ctx context.Context, id domain.ID, at time.Time, ok bool) error
	RemindableEvents(ctx context.Context, from, to time.Time, limit int) ([]domain.Event, error)
	RemindableTodos(ctx context.Context, from, to time.Time, limit int) ([]domain.Todo, error)
	ClaimReminder(ctx context.Context, r domain.Reminder) (bool, error)
	MarkReminderSent(ctx context.Context, r domain.Reminder, sent int) error
	NotifyUser(ctx context.Context, n domain.Notice) error
}

type Options struct {
	Repo    Repo
	Push    *push.Sender
	Clock   clock.Clock
	Logger  *slog.Logger
	Metrics *metrics.Metrics
	BaseURL string
}

type Service struct {
	repo    Repo
	push    *push.Sender
	clock   clock.Clock
	logger  *slog.Logger
	metrics *metrics.Metrics
	base    string
}

type run struct {
	subs  map[domain.ID][]domain.PushSubscription
	users map[domain.ID]domain.User
}

func New(o Options) *Service {
	if o.Clock == nil {
		o.Clock = clock.New()
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Service{repo: o.Repo, push: o.Push, clock: o.Clock, logger: o.Logger, metrics: o.Metrics, base: o.BaseURL}
}

func (s *Service) Handle(ctx context.Context, _ jobs.Job) error {
	now := s.clock.Now().UTC()
	from, to := now.Add(-lookback), now
	ahead := time.Duration(domain.MaxReminderMinutes) * time.Minute
	r := &run{subs: map[domain.ID][]domain.PushSubscription{}, users: map[domain.ID]domain.User{}}

	events, err := s.repo.RemindableEvents(ctx, from.Add(-time.Hour), to.Add(ahead), maxItems)
	if err != nil {
		return err
	}
	for _, e := range events {
		if err := s.event(ctx, r, e, from, to); err != nil {
			return err
		}
	}
	todos, err := s.repo.RemindableTodos(ctx, from.AddDate(0, 0, -1), to.Add(ahead).AddDate(0, 0, 1), maxItems)
	if err != nil {
		return err
	}
	for _, t := range todos {
		if err := s.todo(ctx, r, t, from, to); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) event(ctx context.Context, r *run, e domain.Event, from, to time.Time) error {
	for _, m := range e.Reminders {
		offset := time.Duration(m) * time.Minute
		lo, hi := from.Add(offset), to.Add(offset)
		for _, start := range starts(e, lo, hi) {
			rem := domain.Reminder{
				OwnerID: e.OwnerID, Entity: domain.EntityEvent, EntityID: e.ID,
				OccurrenceAt: start.UTC(), MinutesBefore: m, FireAt: start.Add(-offset).UTC(),
			}
			msg := push.Message{
				Title: title(e.Title),
				Body:  when(e, start, m),
				URL:   s.base + "/events/" + e.ID.String(),
				Tag:   "event-" + e.ID.String(),
			}
			if err := s.deliver(ctx, r, rem, msg); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) todo(ctx context.Context, r *run, t domain.Todo, from, to time.Time) error {
	u, err := s.user(ctx, r, t.OwnerID)
	if err != nil {
		return err
	}
	start, ok := dueAt(t, u)
	if !ok {
		return nil
	}
	for _, m := range t.Reminders {
		fire := start.Add(-time.Duration(m) * time.Minute)
		if !fire.After(from) || fire.After(to) {
			continue
		}
		rem := domain.Reminder{
			OwnerID: t.OwnerID, Entity: domain.EntityTodo, EntityID: t.ID,
			OccurrenceAt: start.UTC(), MinutesBefore: m, FireAt: fire.UTC(),
		}
		msg := push.Message{
			Title: title(t.Title),
			Body:  "Due " + view.Clock(start.In(u.Location()), u.TimeFormat != domain.TimeFormat12),
			URL:   s.base + "/todos/" + t.ID.String(),
			Tag:   "todo-" + t.ID.String(),
		}
		if err := s.deliver(ctx, r, rem, msg); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) deliver(ctx context.Context, r *run, rem domain.Reminder, msg push.Message) error {
	claimed, err := s.repo.ClaimReminder(ctx, rem)
	if err != nil || !claimed {
		return err
	}
	if s.metrics != nil {
		s.metrics.ReminderLag.Observe(s.clock.Now().Sub(rem.FireAt).Seconds())
	}
	if s.push == nil {
		s.inPage(ctx, rem, msg)
		return s.repo.MarkReminderSent(ctx, rem, 0)
	}
	subs, err := s.subscriptions(ctx, r, rem.OwnerID)
	if err != nil {
		return err
	}
	now := s.clock.Now().UTC()
	sent := 0
	for _, sub := range subs {
		err := s.push.Send(ctx, sub, msg)
		switch {
		case err == nil:
			sent++
			_ = s.repo.TouchPushSubscription(ctx, sub.ID, now, true)
		case errors.Is(err, push.ErrGone):
			_ = s.repo.DropPushSubscription(ctx, sub.ID)
		default:
			_ = s.repo.TouchPushSubscription(ctx, sub.ID, now, false)
			s.logger.LogAttrs(ctx, slog.LevelWarn, "push delivery failed", slog.Any("err", err))
		}
		if s.metrics != nil {
			s.metrics.PushSent.WithLabelValues(metrics.Result(err)).Inc()
		}
	}
	s.count(rem.Entity, "push", sent)
	if sent == 0 {
		s.inPage(ctx, rem, msg)
	}
	return s.repo.MarkReminderSent(ctx, rem, sent)
}

// inPage is the fallback when no device took the push: an open tab shows it
// instead, so a reminder is never silently dropped.
func (s *Service) inPage(ctx context.Context, rem domain.Reminder, msg push.Message) {
	err := s.repo.NotifyUser(ctx, domain.Notice{
		OwnerID: rem.OwnerID, Title: msg.Title, Body: msg.Body, URL: msg.URL, Tag: msg.Tag,
	})
	if err != nil {
		s.logger.LogAttrs(ctx, slog.LevelWarn, "in-page reminder failed", slog.Any("err", err))
		return
	}
	s.count(rem.Entity, "inpage", 1)
}

func (s *Service) count(entity, channel string, n int) {
	if s.metrics != nil && n > 0 {
		s.metrics.Reminders.WithLabelValues(entity, channel).Add(float64(n))
	}
}

func (s *Service) subscriptions(ctx context.Context, r *run, owner domain.ID) ([]domain.PushSubscription, error) {
	if subs, ok := r.subs[owner]; ok {
		return subs, nil
	}
	subs, err := s.repo.PushSubscriptions(ctx, owner)
	if err != nil {
		return nil, err
	}
	r.subs[owner] = subs
	return subs, nil
}

func (s *Service) user(ctx context.Context, r *run, id domain.ID) (domain.User, error) {
	if u, ok := r.users[id]; ok {
		return u, nil
	}
	u, err := s.repo.UserByID(ctx, id)
	if err != nil {
		return u, err
	}
	r.users[id] = u
	return u, nil
}

func starts(e domain.Event, lo, hi time.Time) []time.Time {
	if !e.IsMaster() {
		if e.Start.After(lo) && !e.Start.After(hi) {
			return []time.Time{e.Start}
		}
		return nil
	}
	ser, err := recurrence.FromEvent(e)
	if err != nil {
		return nil
	}
	occ, err := ser.Between(lo.Add(-e.Duration()), hi.Add(time.Second), maxOccurs)
	if err != nil {
		return nil
	}
	var out []time.Time
	for _, t := range occ {
		if t.After(lo) && !t.After(hi) {
			out = append(out, t)
		}
	}
	return out
}

func dueAt(t domain.Todo, u domain.User) (time.Time, bool) {
	if t.DueDate == nil {
		return time.Time{}, false
	}
	if t.DueTime != nil {
		at, ok := t.DueStart()
		return at, ok
	}
	d := *t.DueDate
	return time.Date(d.Year(), d.Month(), d.Day(), 0, u.DateOnlyReminder, 0, 0, u.Location()), true
}

func title(s string) string {
	if s == "" {
		return "(no title)"
	}
	return s
}

func when(e domain.Event, start time.Time, minutes int) string {
	if e.AllDay {
		return start.UTC().Format("Mon 2 Jan")
	}
	at := view.Clock(start.In(e.Zone()), true)
	if minutes == 0 {
		return "Starting now, " + at
	}
	return "In " + humanMinutes(minutes) + ", at " + at
}

func humanMinutes(m int) string {
	switch {
	case m%1440 == 0:
		return plural(m/1440, "day")
	case m%60 == 0:
		return plural(m/60, "hour")
	}
	return plural(m, "minute")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}
