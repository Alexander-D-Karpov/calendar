package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

type CalendarRepo interface {
	ListCalendars(ctx context.Context, owner domain.ID) ([]domain.Calendar, error)
	CountCalendars(ctx context.Context, owner domain.ID) (int, error)
	Calendar(ctx context.Context, owner, id domain.ID) (domain.Calendar, error)
	CreateCalendar(ctx context.Context, c domain.Calendar) (domain.Calendar, error)
	UpdateCalendar(ctx context.Context, owner, id domain.ID, fn func(*domain.Calendar) error) (domain.Calendar, error)
	DeleteCalendar(ctx context.Context, owner, id domain.ID, fn func(domain.Calendar) error) error
}

type Calendars struct {
	repo CalendarRepo
}

func NewCalendars(repo CalendarRepo) *Calendars {
	return &Calendars{repo: repo}
}

func (s *Calendars) List(ctx context.Context, owner domain.ID) ([]domain.Calendar, error) {
	return s.repo.ListCalendars(ctx, owner)
}

func (s *Calendars) Get(ctx context.Context, owner, id domain.ID) (domain.Calendar, error) {
	return s.repo.Calendar(ctx, owner, id)
}

func (s *Calendars) Create(ctx context.Context, owner domain.ID, p domain.CalendarPatch) (domain.Calendar, error) {
	n, err := s.repo.CountCalendars(ctx, owner)
	if err != nil {
		return domain.Calendar{}, err
	}
	if n >= domain.MaxCalendars {
		return domain.Calendar{}, fmt.Errorf("%w: an account can have at most %d calendars", domain.ErrConflict, domain.MaxCalendars)
	}
	c := domain.Calendar{
		ID:               domain.NewID(),
		OwnerID:          owner,
		Color:            domain.DefaultCalendarColor,
		Kind:             domain.CalendarLocal,
		Position:         -1,
		DefaultReminders: []int{},
	}
	if err := applyCalendar(&c, p); err != nil {
		return domain.Calendar{}, err
	}
	return s.repo.CreateCalendar(ctx, c)
}

func (s *Calendars) Update(ctx context.Context, owner, id domain.ID, p domain.CalendarPatch, ifMatch string) (domain.Calendar, error) {
	return s.repo.UpdateCalendar(ctx, owner, id, func(c *domain.Calendar) error {
		if err := domain.CheckIfMatch(ifMatch, c.ETag()); err != nil {
			return err
		}
		return applyCalendar(c, p)
	})
}

func (s *Calendars) Delete(ctx context.Context, owner, id domain.ID, ifMatch string) error {
	return s.repo.DeleteCalendar(ctx, owner, id, func(c domain.Calendar) error {
		if c.IsDefault {
			return fmt.Errorf("%w: the default calendar cannot be deleted", domain.ErrConflict)
		}
		return domain.CheckIfMatch(ifMatch, c.ETag())
	})
}

func applyCalendar(c *domain.Calendar, p domain.CalendarPatch) error {
	var v domain.ValidationError
	applyNamed(&v, p.Name, p.Color, p.Position, &c.Name, &c.Color, &c.Position)
	if x, ok := p.Description.Value(&v, "description"); ok {
		c.Description = strings.TrimSpace(x)
	}
	if p.Timezone.Set {
		tz := strings.TrimSpace(p.Timezone.V)
		switch {
		case p.Timezone.Null || tz == "":
			c.Timezone = ""
		case domain.ValidTimezone(tz):
			c.Timezone = tz
		default:
			v.Add("timezone", "unknown timezone")
		}
	}
	if x, ok := p.Hidden.Value(&v, "hidden"); ok {
		c.Hidden = x
	}
	if x, ok := p.DefaultReminders.Value(&v, "default_reminders"); ok {
		c.DefaultReminders = domain.NormalizeReminders(&v, "default_reminders", x)
	}
	v.Length("name", c.Name, 1, 100)
	v.Length("description", c.Description, 0, 2000)
	return v.Err()
}
