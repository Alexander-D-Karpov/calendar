package pg

import (
	"context"

	"github.com/Alexander-D-Karpov/calendar/internal/db/sqlc"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

func (s *Store) ListCalendars(ctx context.Context, owner domain.ID) ([]domain.Calendar, error) {
	rows, err := s.q.ListCalendars(ctx, owner)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]domain.Calendar, len(rows))
	for i, r := range rows {
		out[i] = toCalendar(r)
	}
	return out, nil
}

func (s *Store) CountCalendars(ctx context.Context, owner domain.ID) (int, error) {
	n, err := s.q.CountCalendars(ctx, owner)
	return int(n), mapErr(err)
}

func (s *Store) Calendar(ctx context.Context, owner, id domain.ID) (domain.Calendar, error) {
	row, err := s.q.GetCalendar(ctx, sqlc.GetCalendarParams{ID: id, OwnerID: owner})
	if err != nil {
		return domain.Calendar{}, mapErr(err)
	}
	return toCalendar(row), nil
}

func (s *Store) CreateCalendar(ctx context.Context, c domain.Calendar) (domain.Calendar, error) {
	var out domain.Calendar
	err := s.tx(ctx, func(q *sqlc.Queries) error {
		var err error
		out, err = createCalendar(ctx, q, c)
		return err
	})
	return out, mapErr(err)
}

func createCalendar(ctx context.Context, q *sqlc.Queries, c domain.Calendar) (domain.Calendar, error) {
	if c.Position < 0 {
		next, err := q.NextCalendarPosition(ctx, c.OwnerID)
		if err != nil {
			return domain.Calendar{}, err
		}
		c.Position = int(next)
	}
	row, err := q.CreateCalendar(ctx, sqlc.CreateCalendarParams{
		ID:               c.ID,
		OwnerID:          c.OwnerID,
		Name:             c.Name,
		Color:            c.Color,
		Description:      c.Description,
		Timezone:         textPtr(c.Timezone),
		Kind:             c.Kind,
		ReadOnly:         c.ReadOnly,
		Hidden:           c.Hidden,
		Position:         int32(c.Position),
		DefaultReminders: int32s(c.DefaultReminders),
	})
	if err != nil {
		return domain.Calendar{}, err
	}
	out := toCalendar(row)
	return out, recordChange(ctx, q, calendarChange(out, domain.OpCreate))
}

func (s *Store) UpdateCalendar(ctx context.Context, owner, id domain.ID, fn func(*domain.Calendar) error) (domain.Calendar, error) {
	var out domain.Calendar
	err := s.tx(ctx, func(q *sqlc.Queries) error {
		row, err := q.GetCalendarForUpdate(ctx, sqlc.GetCalendarForUpdateParams{ID: id, OwnerID: owner})
		if err != nil {
			return err
		}
		c := toCalendar(row)
		if err := fn(&c); err != nil {
			return err
		}
		row, err = q.UpdateCalendar(ctx, sqlc.UpdateCalendarParams{
			ID:               id,
			OwnerID:          owner,
			Name:             c.Name,
			Color:            c.Color,
			Description:      c.Description,
			Timezone:         textPtr(c.Timezone),
			Hidden:           c.Hidden,
			Position:         int32(c.Position),
			DefaultReminders: int32s(c.DefaultReminders),
		})
		if err != nil {
			return err
		}
		out = toCalendar(row)
		return recordChange(ctx, q, calendarChange(out, domain.OpUpdate))
	})
	return out, mapErr(err)
}

func (s *Store) DeleteCalendar(ctx context.Context, owner, id domain.ID, fn func(domain.Calendar) error) error {
	return mapErr(s.tx(ctx, func(q *sqlc.Queries) error {
		row, err := q.GetCalendarForUpdate(ctx, sqlc.GetCalendarForUpdateParams{ID: id, OwnerID: owner})
		if err != nil {
			return err
		}
		c := toCalendar(row)
		if err := fn(c); err != nil {
			return err
		}
		if err := affected(q.DeleteCalendar(ctx, sqlc.DeleteCalendarParams{ID: id, OwnerID: owner})); err != nil {
			return err
		}
		return recordChange(ctx, q, calendarChange(c, domain.OpDelete))
	}))
}

func calendarChange(c domain.Calendar, op string) domain.Change {
	id := c.ID
	return domain.Change{OwnerID: c.OwnerID, Entity: domain.EntityCalendar, EntityID: id, Op: op, CalendarID: &id}
}

func toCalendar(r sqlc.Calendar) domain.Calendar {
	return domain.Calendar{
		ID:               r.ID,
		OwnerID:          r.OwnerID,
		Name:             r.Name,
		Color:            r.Color,
		Description:      r.Description,
		Timezone:         text(r.Timezone),
		Kind:             r.Kind,
		ReadOnly:         r.ReadOnly,
		IsDefault:        r.IsDefault,
		Hidden:           r.Hidden,
		Position:         int(r.Position),
		DefaultReminders: ints(r.DefaultReminders),
		CreatedAt:        r.CreatedAt,
		UpdatedAt:        r.UpdatedAt,
	}
}
