package domain

import "time"

const (
	CalendarLocal        = "local"
	CalendarSubscription = "subscription"
	DefaultCalendarColor = "#3b6ea5"
	MaxCalendars         = 100
	MaxCalendarPosition  = 10000
)

type Calendar struct {
	ID               ID
	OwnerID          ID
	Name             string
	Color            string
	Description      string
	Timezone         string
	Kind             string
	ReadOnly         bool
	IsDefault        bool
	Hidden           bool
	Position         int
	DefaultReminders []int
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (c Calendar) ETag() string {
	return TimeTag(c.UpdatedAt)
}

type CalendarPatch struct {
	Name             Opt[string] `json:"name"`
	Color            Opt[string] `json:"color"`
	Description      Opt[string] `json:"description"`
	Timezone         Opt[string] `json:"timezone"`
	Hidden           Opt[bool]   `json:"hidden"`
	Position         Opt[int]    `json:"position"`
	DefaultReminders Opt[[]int]  `json:"default_reminders"`
}
