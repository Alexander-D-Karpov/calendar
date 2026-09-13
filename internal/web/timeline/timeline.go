package timeline

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/recurrence"
	"github.com/Alexander-D-Karpov/calendar/internal/service"
	"github.com/Alexander-D-Karpov/calendar/internal/view"
)

const tooMany = "This period has too many events to show. Pick a shorter view."

type SleepRepo interface {
	SleepSchedule(ctx context.Context, user domain.ID) ([]domain.SleepWindow, error)
}

type ChangeRepo interface {
	LatestSeq(ctx context.Context, owner domain.ID) (int64, error)
	ChangesSince(ctx context.Context, owner domain.ID, since int64, limit int) ([]domain.Change, error)
}

type Source struct {
	Events  *service.Events
	Lists   *service.TodoLists
	Todos   *service.Todos
	Sleep   SleepRepo
	Changes ChangeRepo
}

type Query struct {
	Owner     domain.User
	Loc       *time.Location
	Period    view.Period
	Calendars []domain.Calendar
	Selected  []domain.ID
	Todos     bool
	Sleep     bool
}

type Result struct {
	Items  []view.Item
	Sleep  []domain.SleepWindow
	Seq    int64
	Notice string
}

func (s Source) Load(ctx context.Context, q Query) (Result, error) {
	var res Result
	seq, err := s.Changes.LatestSeq(ctx, q.Owner.ID)
	if err != nil {
		return res, err
	}
	res.Seq = seq
	if len(q.Selected) > 0 {
		from, to := q.Period.Range(q.Loc)
		ids := make([]string, len(q.Selected))
		for i, id := range q.Selected {
			ids[i] = id.String()
		}
		occs, err := s.Events.List(ctx, q.Owner.ID, service.EventQuery{
			From:      from.Format(time.RFC3339),
			To:        to.Format(time.RFC3339),
			Calendars: ids,
			Expand:    true,
		})
		switch {
		case errors.Is(err, recurrence.ErrTooMany):
			res.Notice = tooMany
		case err != nil:
			return res, err
		default:
			res.Items = view.FromOccurrences(occs, q.Calendars)
		}
	}
	if q.Todos {
		lists, err := s.Lists.List(ctx, q.Owner.ID)
		if err != nil {
			return res, err
		}
		todos, err := s.Todos.Scheduled(ctx, q.Owner.ID, q.Period.Start, q.Period.End)
		if err != nil {
			return res, err
		}
		res.Items = append(res.Items, view.FromTodos(todos, lists)...)
	}
	if q.Sleep && q.Owner.SleepEnabled && q.Period.Kind.Timed() {
		if res.Sleep, err = s.Sleep.SleepSchedule(ctx, q.Owner.ID); err != nil {
			return res, err
		}
	}
	return res, nil
}

func LiveURL(base string, p view.Period, loc *time.Location) string {
	from, to := p.Range(loc)
	q := url.Values{"from": {from.UTC().Format(time.RFC3339)}, "to": {to.UTC().Format(time.RFC3339)}}
	return base + "?" + q.Encode()
}
