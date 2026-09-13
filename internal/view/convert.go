package view

import (
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/markdown"
)

const ExcerptRunes = 200

func FromOccurrences(occs []domain.Occurrence, cals []domain.Calendar) []Item {
	byID := make(map[domain.ID]domain.Calendar, len(cals))
	for _, c := range cals {
		byID[c.ID] = c
	}
	out := make([]Item, 0, len(occs))
	for _, o := range occs {
		e := o.Event
		c := byID[e.CalendarID]
		color := e.Color
		if color == "" {
			color = c.Color
		}
		it := Item{
			ID:           e.ID.String(),
			Title:        e.Title,
			Location:     e.Location,
			Excerpt:      markdown.Plain(e.Body, ExcerptRunes),
			Color:        Hex(color),
			CalendarName: c.Name,
			AllDay:       e.AllDay,
			Tentative:    e.Status == domain.StatusTentative,
			Private:      e.Visibility == domain.VisibilityPrivate,
			ReadOnly:     c.ReadOnly || e.ReadOnly,
			Start:        e.Start,
			End:          e.End,
		}
		if o.Instance != nil && !e.IsOverride() {
			it.Instance = InstanceParam(e, *o.Instance)
		}
		out = append(out, it)
	}
	return out
}

func FromTodos(todos []domain.Todo, lists []domain.TodoList) []Item {
	byID := make(map[domain.ID]domain.TodoList, len(lists))
	for _, l := range lists {
		byID[l.ID] = l
	}
	out := make([]Item, 0, len(todos))
	for _, t := range todos {
		start, end, allDay, ok := t.Span()
		if !ok {
			continue
		}
		l := byID[t.ListID]
		out = append(out, Item{
			ID:           t.ID.String(),
			Title:        t.Title,
			Excerpt:      markdown.Plain(t.Body, ExcerptRunes),
			Color:        Hex(l.Color),
			CalendarName: l.Name,
			AllDay:       allDay,
			Todo:         true,
			Done:         t.Done(),
			ChecksDone:   t.ChecksDone(),
			ChecksTotal:  len(t.Checks),
			Start:        start,
			End:          end,
		})
	}
	return out
}

func Redact(items []Item, detail string) []Item {
	out := make([]Item, 0, len(items))
	for _, it := range items {
		if it.Private {
			continue
		}
		it.Static = true
		switch detail {
		case domain.DetailFull:
		case domain.DetailTitles:
			it.Location, it.Excerpt = "", ""
		default:
			it.Title, it.Location, it.Excerpt, it.CalendarName = "Busy", "", "", ""
			it.ChecksDone, it.ChecksTotal, it.Tentative = 0, 0, false
		}
		out = append(out, it)
	}
	return out
}

func Colors(items []Item, cals []domain.Calendar) []string {
	set := map[string]bool{}
	for _, it := range items {
		if it.Color != "" {
			set[it.Color] = true
		}
	}
	for _, c := range cals {
		set[Hex(c.Color)] = true
	}
	return slices.Sorted(maps.Keys(set))
}

func InstanceParam(e domain.Event, t time.Time) string {
	if e.AllDay {
		return t.UTC().Format(domain.DateLayout)
	}
	return t.UTC().Format(time.RFC3339)
}

func Hex(color string) string {
	return strings.TrimPrefix(color, "#")
}

func ParseInstance(s string, allDay bool) (time.Time, bool) {
	layout := time.RFC3339
	if allDay {
		layout = domain.DateLayout
	}
	t, err := time.Parse(layout, s)
	return t, err == nil
}
