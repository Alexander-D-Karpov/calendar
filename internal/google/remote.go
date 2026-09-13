package google

import (
	"context"
	"errors"
	"time"
)

var (
	ErrGone         = errors.New("google: sync token expired")
	ErrNotFound     = errors.New("google: not found")
	ErrPrecondition = errors.New("google: resource changed")
	ErrRevoked      = errors.New("google: access revoked")
)

const (
	StatusConfirmed = "confirmed"
	StatusTentative = "tentative"
	StatusCancelled = "cancelled"
	TaskOpen        = "needsAction"
	TaskCompleted   = "completed"
)

type When struct {
	Date     string `json:"date,omitempty"`
	DateTime string `json:"date_time,omitempty"`
	TimeZone string `json:"time_zone,omitempty"`
}

type Event struct {
	ID                  string    `json:"id"`
	ICalUID             string    `json:"ical_uid,omitempty"`
	ETag                string    `json:"etag"`
	Status              string    `json:"status"`
	Summary             string    `json:"summary"`
	Description         string    `json:"description,omitempty"`
	Location            string    `json:"location,omitempty"`
	Start               When      `json:"start"`
	End                 When      `json:"end"`
	Recurrence          []string  `json:"recurrence,omitempty"`
	RecurringEventID    string    `json:"recurring_event_id,omitempty"`
	OriginalStart       When      `json:"original_start"`
	Transparency        string    `json:"transparency,omitempty"`
	Visibility          string    `json:"visibility,omitempty"`
	Reminders           []int     `json:"reminders,omitempty"`
	UseDefaultReminders bool      `json:"use_default_reminders"`
	Editable            bool      `json:"editable"`
	Updated             time.Time `json:"updated"`
	SendRecurrence      bool      `json:"-"`
}

type EventDelta struct {
	Items     []Event
	SyncToken string
}

type Calendar struct {
	ID         string
	Name       string
	Color      string
	AccessRole string
	TimeZone   string
	Primary    bool
}

func (c Calendar) ReadOnly() bool {
	return c.AccessRole != "owner" && c.AccessRole != "writer"
}

type TaskList struct {
	ID   string
	Name string
}

type Task struct {
	ID        string     `json:"id"`
	ETag      string     `json:"etag"`
	Title     string     `json:"title"`
	Notes     string     `json:"notes,omitempty"`
	Status    string     `json:"status"`
	Parent    string     `json:"parent,omitempty"`
	Due       string     `json:"due,omitempty"`
	Completed *time.Time `json:"completed,omitempty"`
	Deleted   bool       `json:"deleted,omitempty"`
	Updated   time.Time  `json:"updated"`
}

type Remote interface {
	Calendars(ctx context.Context) ([]Calendar, error)
	TaskLists(ctx context.Context) ([]TaskList, error)
	Events(ctx context.Context, cal, syncToken string) (EventDelta, error)
	Event(ctx context.Context, cal, id string) (Event, error)
	Instance(ctx context.Context, cal, master, originalStart string) (Event, error)
	InsertEvent(ctx context.Context, cal string, e Event) (Event, error)
	PatchEvent(ctx context.Context, cal string, e Event, ifMatch string) (Event, error)
	DeleteEvent(ctx context.Context, cal, id string) error
	Watch(ctx context.Context, cal, channel, token, address string) (string, time.Time, error)
	StopWatch(ctx context.Context, channel, resource string) error
	Tasks(ctx context.Context, list string, updatedMin *time.Time) ([]Task, error)
	Task(ctx context.Context, list, id string) (Task, error)
	InsertTask(ctx context.Context, list string, t Task) (Task, error)
	PatchTask(ctx context.Context, list string, t Task) (Task, error)
	MoveTask(ctx context.Context, list, id, parent string) (Task, error)
	DeleteTask(ctx context.Context, list, id string) error
}
