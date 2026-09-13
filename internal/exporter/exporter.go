package exporter

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/csvfmt"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/ical"
	"github.com/Alexander-D-Karpov/calendar/internal/recurrence"
	"github.com/Alexander-D-Karpov/calendar/internal/store"
	"github.com/Alexander-D-Karpov/calendar/internal/transfer"
	"github.com/Alexander-D-Karpov/calendar/internal/yamlfmt"
)

type Repo interface {
	UserByID(ctx context.Context, id domain.ID) (domain.User, error)
	ListCalendars(ctx context.Context, owner domain.ID) ([]domain.Calendar, error)
	ListTodoLists(ctx context.Context, owner domain.ID) ([]domain.TodoList, error)
	CalendarEvents(ctx context.Context, owner, calendar domain.ID) ([]domain.Event, error)
	Todos(ctx context.Context, owner domain.ID, f store.TodoFilter) ([]domain.Todo, error)
}

type Service struct {
	repo Repo
}

func New(repo Repo) *Service {
	return &Service{repo: repo}
}

type File struct {
	Name        string
	ContentType string
	Body        []byte
}

func (s *Service) Calendars(ctx context.Context, owner domain.ID, ids []domain.ID) (transfer.Set, error) {
	var set transfer.Set
	cals, err := s.repo.ListCalendars(ctx, owner)
	if err != nil {
		return set, err
	}
	for _, c := range cals {
		if len(ids) > 0 && !slices.Contains(ids, c.ID) {
			continue
		}
		events, err := s.repo.CalendarEvents(ctx, owner, c.ID)
		if err != nil {
			return set, err
		}
		set.Calendars = append(set.Calendars, transfer.Calendar{
			Name: c.Name, Color: c.Color, Timezone: c.Timezone, Description: c.Description, Hidden: c.Hidden,
			Reminders: c.DefaultReminders, Series: group(events),
		})
	}
	return set, nil
}

func group(events []domain.Event) []transfer.Series {
	var out []transfer.Series
	index := map[domain.ID]int{}
	for _, e := range events {
		if e.SeriesID == nil {
			index[e.ID] = len(out)
			out = append(out, transfer.Series{Master: e, Modified: e.UpdatedAt})
		}
	}
	for _, e := range events {
		if e.SeriesID == nil {
			continue
		}
		if i, ok := index[*e.SeriesID]; ok {
			out[i].Overrides = append(out[i].Overrides, e)
		}
	}
	return out
}

func (s *Service) Lists(ctx context.Context, owner domain.ID, ids []domain.ID) (transfer.Set, error) {
	var set transfer.Set
	lists, err := s.repo.ListTodoLists(ctx, owner)
	if err != nil {
		return set, err
	}
	for _, l := range lists {
		if len(ids) > 0 && !slices.Contains(ids, l.ID) {
			continue
		}
		todos, err := s.repo.Todos(ctx, owner, store.TodoFilter{Lists: []domain.ID{l.ID}, Order: store.OrderByPosition})
		if err != nil {
			return set, err
		}
		set.Lists = append(set.Lists, transfer.List{Name: l.Name, Color: l.Color, Todos: nest(todos)})
	}
	return set, nil
}

func nest(todos []domain.Todo) []transfer.TodoNode {
	var out []transfer.TodoNode
	index := map[domain.ID]int{}
	for _, t := range todos {
		if t.ParentID == nil {
			index[t.ID] = len(out)
			out = append(out, transfer.TodoNode{Todo: t, Modified: t.UpdatedAt})
		}
	}
	for _, t := range todos {
		if t.ParentID == nil {
			continue
		}
		if i, ok := index[*t.ParentID]; ok {
			out[i].Subtasks = append(out[i].Subtasks, transfer.TodoNode{Todo: t, Modified: t.UpdatedAt})
		}
	}
	return out
}

func (s *Service) ICS(ctx context.Context, owner domain.ID, ids []domain.ID) (File, error) {
	set, err := s.Calendars(ctx, owner, ids)
	if err != nil {
		return File{}, err
	}
	name := "calendar"
	if len(set.Calendars) == 1 {
		name = set.Calendars[0].Name
	}
	body, err := ical.Encode(set, name)
	return File{Name: filename(name, "ics"), ContentType: "text/calendar; charset=utf-8", Body: body}, err
}

func (s *Service) CSV(ctx context.Context, owner domain.ID, ids []domain.ID) (File, error) {
	set, err := s.Calendars(ctx, owner, ids)
	if err != nil {
		return File{}, err
	}
	name := "calendar"
	if len(set.Calendars) == 1 {
		name = set.Calendars[0].Name
	}
	body, err := csvfmt.Encode(set)
	return File{Name: filename(name, "csv"), ContentType: "text/csv; charset=utf-8", Body: body}, err
}

func (s *Service) YAML(ctx context.Context, owner domain.ID) (File, error) {
	u, err := s.repo.UserByID(ctx, owner)
	if err != nil {
		return File{}, err
	}
	set, err := s.Calendars(ctx, owner, nil)
	if err != nil {
		return File{}, err
	}
	lists, err := s.Lists(ctx, owner, nil)
	if err != nil {
		return File{}, err
	}
	set.Lists = lists.Lists
	body, err := yamlfmt.Encode(set, &u)
	return File{Name: filename("account", "yaml"), ContentType: "application/yaml; charset=utf-8", Body: body}, err
}

func filename(name, ext string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		case r == ' ':
			return '-'
		}
		return -1
	}, name)
	clean = strings.Trim(clean, "-")
	if clean == "" {
		clean = "export"
	}
	return fmt.Sprintf("%s-%s.%s", transfer.Clip(clean, 60), time.Now().UTC().Format("20060102"), ext)
}

// Range is the feed view of an account: only what overlaps the window, and
// never anything marked private, because the caller holds a share link rather
// than the account.
func (s *Service) Range(ctx context.Context, owner domain.ID, ids []domain.ID, from, to time.Time, todos bool) (transfer.Set, error) {
	var set transfer.Set
	cals, err := s.repo.ListCalendars(ctx, owner)
	if err != nil {
		return set, err
	}
	for _, c := range cals {
		if len(ids) > 0 && !slices.Contains(ids, c.ID) {
			continue
		}
		events, err := s.repo.CalendarEvents(ctx, owner, c.ID)
		if err != nil {
			return set, err
		}
		kept := make([]domain.Event, 0, len(events))
		for _, e := range events {
			if e.Visibility == domain.VisibilityPrivate || !overlaps(e, from, to) {
				continue
			}
			kept = append(kept, e)
		}
		if len(kept) == 0 {
			continue
		}
		set.Calendars = append(set.Calendars, transfer.Calendar{
			Name: c.Name, Color: c.Color, Timezone: c.Timezone, Description: c.Description, Hidden: c.Hidden,
			Reminders: c.DefaultReminders, Series: group(kept),
		})
	}
	if !todos {
		return set, nil
	}
	lists, err := s.repo.ListTodoLists(ctx, owner)
	if err != nil {
		return set, err
	}
	for _, l := range lists {
		items, err := s.repo.Todos(ctx, owner, store.TodoFilter{
			Lists: []domain.ID{l.ID}, Scheduled: true, DueFrom: &from, DueTo: &to, Order: store.OrderByDue,
		})
		if err != nil {
			return set, err
		}
		if len(items) == 0 {
			continue
		}
		set.Lists = append(set.Lists, transfer.List{Name: l.Name, Color: l.Color, Todos: nest(items)})
	}
	return set, nil
}

// A recurring master has no end, so it overlaps any window that starts after it.
func overlaps(e domain.Event, from, to time.Time) bool {
	start, end := recurrence.Span(e)
	if !start.Before(to) {
		return false
	}
	return end == nil || end.After(from)
}
