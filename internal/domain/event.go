package domain

import "time"

const (
	StatusConfirmed = "confirmed"
	StatusTentative = "tentative"
	StatusCancelled = "cancelled"

	TransparencyOpaque      = "opaque"
	TransparencyTransparent = "transparent"

	VisibilityDefault = "default"
	VisibilityPrivate = "private"

	ScopeThis      = "this"
	ScopeFollowing = "following"
	ScopeAll       = "all"

	DateLayout       = "2006-01-02"
	TimeLayout       = "15:04"
	MaxEventDuration = 366 * 24 * time.Hour

	// SEQUENCE arrives from remote feeds and uploads and is stored in an
	// int32 column, so it is clamped on the way in rather than silently
	// wrapping negative. RFC 5545 sets no upper bound; no real calendar
	// revises an event this many times.
	MaxSequence = 1 << 24
)

type Event struct {
	ID           ID
	OwnerID      ID
	CalendarID   ID
	UID          string
	SeriesID     *ID
	RecurrenceID *time.Time
	Title        string
	Body         string
	Location     string
	URL          string
	AllDay       bool
	Start        time.Time
	End          time.Time
	TZ           string
	RRule        string
	RDate        []time.Time
	ExDate       []time.Time
	Status       string
	Transparency string
	Visibility   string
	Color        string
	ReadOnly     bool
	Sequence     int
	Version      int64
	Reminders    []int
	SourceHash   []byte
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    *time.Time
}

func (e Event) ETag() string {
	return VersionTag(e.Version)
}

func (e Event) IsMaster() bool {
	return e.SeriesID == nil && (e.RRule != "" || len(e.RDate) > 0)
}

func (e Event) IsOverride() bool {
	return e.SeriesID != nil
}

func (e Event) Duration() time.Duration {
	return e.End.Sub(e.Start)
}

func (e Event) Zone() *time.Location {
	if e.AllDay || e.TZ == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(e.TZ)
	if err != nil {
		return time.UTC
	}
	return loc
}

type Occurrence struct {
	Event    Event
	Instance *time.Time
}

type EventPatch struct {
	CalendarID   Opt[string]   `json:"calendar_id"`
	UID          Opt[string]   `json:"uid"`
	Title        Opt[string]   `json:"title"`
	Body         Opt[string]   `json:"body"`
	Location     Opt[string]   `json:"location"`
	URL          Opt[string]   `json:"url"`
	AllDay       Opt[bool]     `json:"all_day"`
	Start        Opt[string]   `json:"start"`
	End          Opt[string]   `json:"end"`
	Timezone     Opt[string]   `json:"timezone"`
	RRule        Opt[string]   `json:"rrule"`
	RDate        Opt[[]string] `json:"rdate"`
	ExDate       Opt[[]string] `json:"exdate"`
	Status       Opt[string]   `json:"status"`
	Transparency Opt[string]   `json:"transparency"`
	Visibility   Opt[string]   `json:"visibility"`
	Color        Opt[string]   `json:"color"`
	Reminders    Opt[[]int]    `json:"reminders"`
}
