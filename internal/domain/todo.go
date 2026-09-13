package domain

import (
	"fmt"
	"strings"
	"time"
)

const (
	TodoOpen           = "needs_action"
	TodoCompleted      = "completed"
	MaxTodoLists       = 100
	MaxChecks          = 200
	MaxTodoPriority    = 3
	DefaultTodoMinutes = 30
	DefaultListColor   = "#5b7c5a"
)

type TodoList struct {
	ID        ID
	OwnerID   ID
	Name      string
	Color     string
	Position  int
	IsDefault bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (l TodoList) ETag() string {
	return TimeTag(l.UpdatedAt)
}

type TodoListPatch struct {
	Name     Opt[string] `json:"name"`
	Color    Opt[string] `json:"color"`
	Position Opt[int]    `json:"position"`
}

type Check struct {
	ID        ID
	TodoID    ID
	Text      string
	Done      bool
	Position  string
	DoneAt    *time.Time
	CreatedAt time.Time
}

type CheckPatch struct {
	Text Opt[string] `json:"text"`
	Done Opt[bool]   `json:"done"`
}

type CheckInput struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

type Todo struct {
	ID             ID
	OwnerID        ID
	ListID         ID
	ParentID       *ID
	UID            string
	Title          string
	Body           string
	Status         string
	Priority       int
	Position       string
	DueDate        *time.Time
	DueTime        *int
	Duration       int
	TZ             string
	RRule          string
	ShowOnCalendar bool
	CompletedAt    *time.Time
	Version        int64
	Reminders      []int
	Checks         []Check
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeletedAt      *time.Time
}

func (t Todo) ETag() string {
	return VersionTag(t.Version)
}

func (t Todo) Done() bool {
	return t.Status == TodoCompleted
}

func (t Todo) Zone() *time.Location {
	if t.TZ == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(t.TZ)
	if err != nil {
		return time.UTC
	}
	return loc
}

func (t Todo) DueStart() (time.Time, bool) {
	if t.DueDate == nil {
		return time.Time{}, false
	}
	d := *t.DueDate
	if t.DueTime == nil {
		return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC), true
	}
	return time.Date(d.Year(), d.Month(), d.Day(), 0, *t.DueTime, 0, 0, t.Zone()), true
}

func (t Todo) Span() (start, end time.Time, allDay, ok bool) {
	start, ok = t.DueStart()
	if !ok {
		return
	}
	if t.DueTime == nil {
		return start, start.AddDate(0, 0, 1), true, true
	}
	mins := t.Duration
	if mins <= 0 {
		mins = DefaultTodoMinutes
	}
	return start, start.Add(time.Duration(mins) * time.Minute), false, true
}

func (t Todo) ChecksDone() int {
	n := 0
	for _, c := range t.Checks {
		if c.Done {
			n++
		}
	}
	return n
}

type TodoPatch struct {
	ListID         Opt[string]       `json:"list_id"`
	ParentID       Opt[string]       `json:"parent_id"`
	Title          Opt[string]       `json:"title"`
	Body           Opt[string]       `json:"body"`
	Priority       Opt[int]          `json:"priority"`
	DueDate        Opt[string]       `json:"due_date"`
	DueTime        Opt[string]       `json:"due_time"`
	DurationMin    Opt[int]          `json:"duration_min"`
	Timezone       Opt[string]       `json:"timezone"`
	RRule          Opt[string]       `json:"rrule"`
	ShowOnCalendar Opt[bool]         `json:"show_on_calendar"`
	Reminders      Opt[[]int]        `json:"reminders"`
	Checks         Opt[[]CheckInput] `json:"checks"`
}

type TodoMove struct {
	ListID   Opt[string] `json:"list_id"`
	ParentID Opt[string] `json:"parent_id"`
	AfterID  Opt[string] `json:"after_id"`
}

func ParseClock(s string) (int, bool) {
	for _, l := range []string{"15:04", "15:04:05"} {
		if t, err := time.Parse(l, strings.TrimSpace(s)); err == nil {
			return t.Hour()*60 + t.Minute(), true
		}
	}
	return 0, false
}

func FormatClock(m int) string {
	return fmt.Sprintf("%02d:%02d", m/60, m%60)
}
