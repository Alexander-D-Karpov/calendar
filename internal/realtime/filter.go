package realtime

import (
	"slices"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

type Action int

const (
	Skip Action = iota
	Refresh
	Reload
)

type Filter func(domain.Change) Action

func Overlaps(c domain.Change, from, to time.Time) bool {
	switch {
	case c.From == nil, !c.From.Before(to):
		return false
	case c.To == nil:
		return true
	}
	return c.To.After(from) || (!c.To.After(*c.From) && !c.From.Before(from))
}

func RangeFilter(from, to time.Time) Filter {
	return func(c domain.Change) Action {
		switch c.Entity {
		case domain.EntityEvent, domain.EntityTodo:
			if Overlaps(c, from, to) {
				return Refresh
			}
		case domain.EntityCalendar, domain.EntityList, domain.EntitySettings:
			return Refresh
		}
		return Skip
	}
}

func ShareFilter(share domain.ID, cals []domain.ID, todos bool, from, to time.Time) Filter {
	return func(c domain.Change) Action {
		switch c.Entity {
		case domain.EntityShare:
			if c.EntityID == share {
				return Reload
			}
		case domain.EntityCalendar:
			if slices.Contains(cals, c.EntityID) {
				return Refresh
			}
		case domain.EntityEvent:
			if c.CalendarID != nil && slices.Contains(cals, *c.CalendarID) && Overlaps(c, from, to) {
				return Refresh
			}
		case domain.EntityTodo:
			if todos && Overlaps(c, from, to) {
				return Refresh
			}
		case domain.EntityList:
			if todos {
				return Refresh
			}
		case domain.EntitySettings:
			return Refresh
		}
		return Skip
	}
}
