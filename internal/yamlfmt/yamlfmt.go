package yamlfmt

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/recurrence"
	"github.com/Alexander-D-Karpov/calendar/internal/transfer"
)

const (
	Format = "calendar"
	Schema = 1
)

type File struct {
	Format     string     `yaml:"format"`
	Schema     int        `yaml:"schema"`
	ExportedAt string     `yaml:"exported_at,omitempty"`
	Settings   *Settings  `yaml:"settings,omitempty"`
	Calendars  []Calendar `yaml:"calendars,omitempty"`
	TodoLists  []List     `yaml:"todo_lists,omitempty"`
}

type Settings struct {
	Timezone         string `yaml:"timezone,omitempty"`
	WeekStart        string `yaml:"week_start,omitempty"`
	TimeFormat       string `yaml:"time_format,omitempty"`
	DefaultView      string `yaml:"default_view,omitempty"`
	DateOnlyReminder string `yaml:"date_only_reminder,omitempty"`
}

type Calendar struct {
	Name        string  `yaml:"name"`
	Color       string  `yaml:"color,omitempty"`
	Timezone    string  `yaml:"timezone,omitempty"`
	Description string  `yaml:"description,omitempty"`
	Hidden      bool    `yaml:"hidden,omitempty"`
	Reminders   []int   `yaml:"default_reminders,omitempty"`
	Events      []Event `yaml:"events,omitempty"`
}

type Event struct {
	UID          string     `yaml:"uid,omitempty"`
	Title        string     `yaml:"title,omitempty"`
	Body         string     `yaml:"body,omitempty"`
	Location     string     `yaml:"location,omitempty"`
	URL          string     `yaml:"url,omitempty"`
	AllDay       bool       `yaml:"all_day,omitempty"`
	Start        string     `yaml:"start"`
	End          string     `yaml:"end,omitempty"`
	Timezone     string     `yaml:"timezone,omitempty"`
	RRule        string     `yaml:"rrule,omitempty"`
	RDates       []string   `yaml:"rdates,omitempty"`
	ExDates      []string   `yaml:"exdates,omitempty"`
	Status       string     `yaml:"status,omitempty"`
	Transparency string     `yaml:"transparency,omitempty"`
	Visibility   string     `yaml:"visibility,omitempty"`
	Color        string     `yaml:"color,omitempty"`
	Reminders    []int      `yaml:"reminders,omitempty"`
	Overrides    []Override `yaml:"overrides,omitempty"`
}

type Override struct {
	RecurrenceID string `yaml:"recurrence_id"`
	Event        `yaml:",inline"`
}

type List struct {
	Name  string `yaml:"name"`
	Color string `yaml:"color,omitempty"`
	Todos []Todo `yaml:"todos,omitempty"`
}

type Todo struct {
	UID            string  `yaml:"uid,omitempty"`
	Title          string  `yaml:"title"`
	Body           string  `yaml:"body,omitempty"`
	Status         string  `yaml:"status,omitempty"`
	Priority       int     `yaml:"priority,omitempty"`
	Due            string  `yaml:"due,omitempty"`
	Time           string  `yaml:"time,omitempty"`
	Duration       string  `yaml:"duration,omitempty"`
	Timezone       string  `yaml:"timezone,omitempty"`
	RRule          string  `yaml:"rrule,omitempty"`
	ShowOnCalendar *bool   `yaml:"show_on_calendar,omitempty"`
	CompletedAt    string  `yaml:"completed_at,omitempty"`
	Reminders      []int   `yaml:"reminders,omitempty"`
	Checks         []Check `yaml:"checks,omitempty"`
	Subtasks       []Todo  `yaml:"subtasks,omitempty"`
}

type Check struct {
	Text string `yaml:"text"`
	Done bool   `yaml:"done,omitempty"`
}

type Options struct {
	Timezone string
	MaxItems int
}

var weekdays = []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}

func Decode(data []byte, o Options) (transfer.Set, error) {
	var set transfer.Set
	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return set, fmt.Errorf("%w: the file is empty", domain.ErrInvalid)
		}
		return set, fmt.Errorf("%w: %s", domain.ErrInvalid, strings.TrimPrefix(err.Error(), "yaml: "))
	}
	if err := dec.Decode(&yaml.Node{}); !errors.Is(err, io.EOF) {
		return set, fmt.Errorf("%w: the file must hold a single document", domain.ErrInvalid)
	}
	if err := measure(&doc); err != nil {
		return set, err
	}
	// The node walk above bounds the expansion; this pass re-reads the bytes so
	// that KnownFields still rejects a stray key, which Node.Decode ignores.
	var f File
	strict := yaml.NewDecoder(bytes.NewReader(data))
	strict.KnownFields(true)
	if err := strict.Decode(&f); err != nil {
		return set, fmt.Errorf("%w: %s", domain.ErrInvalid, strings.TrimPrefix(err.Error(), "yaml: "))
	}
	if f.Format != Format {
		return set, fmt.Errorf("%w: this is not a calendar export, format is %q", domain.ErrInvalid, f.Format)
	}
	if f.Schema != Schema {
		return set, fmt.Errorf("%w: schema %d is not supported, this build reads schema %d", domain.ErrInvalid, f.Schema, Schema)
	}
	if s := f.Settings; s != nil {
		set.Settings = settings(&set, *s)
	}
	count := 0
	for ci, c := range f.Calendars {
		out := transfer.Calendar{
			Name: c.Name, Color: c.Color, Timezone: c.Timezone, Description: c.Description, Hidden: c.Hidden, Reminders: c.Reminders,
		}
		tz := o.Timezone
		if c.Timezone != "" {
			tz = c.Timezone
		}
		for ei, ev := range c.Events {
			count++
			if o.MaxItems > 0 && count > o.MaxItems {
				return set, fmt.Errorf("%w: the file has more than %d entries", domain.ErrInvalid, o.MaxItems)
			}
			where := fmt.Sprintf("calendar %d, event %d", ci+1, ei+1)
			if ev.Title != "" {
				where = "event " + strconv.Quote(transfer.Clip(ev.Title, 60))
			}
			s, err := series(ev, tz)
			if err != nil {
				set.Problem(where, "%s", err)
				continue
			}
			s.Where = where
			count += len(s.Overrides)
			out.Series = append(out.Series, s)
		}
		set.Calendars = append(set.Calendars, out)
	}
	for li, l := range f.TodoLists {
		out := transfer.List{Name: l.Name, Color: l.Color}
		for ti, td := range l.Todos {
			count++
			if o.MaxItems > 0 && count > o.MaxItems {
				return set, fmt.Errorf("%w: the file has more than %d entries", domain.ErrInvalid, o.MaxItems)
			}
			where := fmt.Sprintf("list %d, todo %d", li+1, ti+1)
			if td.Title != "" {
				where = "todo " + strconv.Quote(transfer.Clip(td.Title, 60))
			}
			n, err := node(td, o.Timezone, true)
			if err != nil {
				set.Problem(where, "%s", err)
				continue
			}
			n.Where = where
			count += len(n.Subtasks)
			out.Todos = append(out.Todos, n)
		}
		set.Lists = append(set.Lists, out)
	}
	if len(set.Calendars) == 0 && len(set.Lists) == 0 {
		return set, fmt.Errorf("%w: the file has no calendars or lists", domain.ErrInvalid)
	}
	return set, nil
}

func settings(set *transfer.Set, s Settings) *domain.SettingsPatch {
	p := &domain.SettingsPatch{}
	if s.Timezone != "" {
		if domain.ValidTimezone(s.Timezone) {
			p.Timezone = domain.Some(s.Timezone)
		} else {
			set.Problem("settings", "timezone %q is unknown", s.Timezone)
		}
	}
	if s.WeekStart != "" {
		if i := slices.Index(weekdays, strings.ToLower(s.WeekStart)); i >= 0 {
			p.WeekStart = domain.Some(i)
		} else {
			set.Problem("settings", "week_start %q is not a weekday", s.WeekStart)
		}
	}
	if slices.Contains(domain.TimeFormats, s.TimeFormat) {
		p.TimeFormat = domain.Some(s.TimeFormat)
	}
	if slices.Contains(domain.Views, s.DefaultView) {
		p.DefaultView = domain.Some(s.DefaultView)
	}
	if s.DateOnlyReminder != "" {
		if _, ok := domain.ParseClock(s.DateOnlyReminder); ok {
			p.DateOnlyReminder = domain.Some(s.DateOnlyReminder)
		} else {
			set.Problem("settings", "date_only_reminder %q is not a time", s.DateOnlyReminder)
		}
	}
	return p
}

func parseStamp(v, tz string, allDay bool) (time.Time, error) {
	v = strings.TrimSpace(v)
	if allDay {
		return time.Parse(domain.DateLayout, v)
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC(), nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	for _, l := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(l, v, loc); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("is not a date and time")
}

func event(in Event, tz string) (domain.Event, error) {
	if in.Timezone != "" {
		tz = in.Timezone
	}
	if !domain.ValidTimezone(tz) {
		tz = "UTC"
	}
	e := domain.Event{
		UID: in.UID, Title: in.Title, Body: in.Body, Location: in.Location, URL: in.URL, AllDay: in.AllDay,
		Status: domain.StatusConfirmed, Transparency: domain.TransparencyOpaque, Visibility: domain.VisibilityDefault,
		Reminders: in.Reminders,
	}
	if !in.AllDay {
		e.TZ = tz
	}
	start, err := parseStamp(in.Start, tz, in.AllDay)
	if err != nil {
		return e, fmt.Errorf("start %q %w", in.Start, err)
	}
	e.Start = start
	e.End = start.Add(time.Hour)
	if in.AllDay {
		e.End = start.AddDate(0, 0, 1)
	}
	if in.End != "" {
		end, err := parseStamp(in.End, tz, in.AllDay)
		if err != nil {
			return e, fmt.Errorf("end %q %w", in.End, err)
		}
		e.End = end
	}
	if slices.Contains([]string{domain.StatusConfirmed, domain.StatusTentative, domain.StatusCancelled}, in.Status) {
		e.Status = in.Status
	}
	if in.Transparency == domain.TransparencyTransparent {
		e.Transparency = domain.TransparencyTransparent
	}
	if in.Visibility == domain.VisibilityPrivate {
		e.Visibility = domain.VisibilityPrivate
	}
	if c, ok := domain.NormalizeColor(in.Color); in.Color != "" && ok {
		e.Color = c
	}
	if in.RRule != "" {
		n, err := recurrence.Normalize(in.RRule, e.Zone())
		if err != nil {
			return e, fmt.Errorf("rrule %w", err)
		}
		e.RRule = n
	}
	if e.RDate, err = stamps(in.RDates, tz, in.AllDay); err != nil {
		return e, fmt.Errorf("rdates: %w", err)
	}
	if e.ExDate, err = stamps(in.ExDates, tz, in.AllDay); err != nil {
		return e, fmt.Errorf("exdates: %w", err)
	}
	return e, nil
}

func stamps(in []string, tz string, allDay bool) ([]time.Time, error) {
	out := make([]time.Time, 0, len(in))
	for _, v := range in {
		t, err := parseStamp(v, tz, allDay)
		if err != nil {
			return nil, fmt.Errorf("%q %w", v, err)
		}
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b time.Time) int { return a.Compare(b) })
	return slices.CompactFunc(out, func(a, b time.Time) bool { return a.Equal(b) }), nil
}

func series(in Event, tz string) (transfer.Series, error) {
	master, err := event(in, tz)
	if err != nil {
		return transfer.Series{}, err
	}
	s := transfer.Series{Master: master}
	for _, ov := range in.Overrides {
		if ov.Start == "" {
			ov.Start = ov.RecurrenceID
		}
		if ov.Timezone == "" {
			ov.Timezone = in.Timezone
		}
		ov.AllDay = ov.AllDay || in.AllDay
		e, err := event(ov.Event, tz)
		if err != nil {
			return s, fmt.Errorf("override: %w", err)
		}
		rid, err := parseStamp(ov.RecurrenceID, tz, in.AllDay)
		if err != nil {
			return s, fmt.Errorf("override recurrence_id %q %w", ov.RecurrenceID, err)
		}
		e.UID, e.RecurrenceID, e.RRule, e.RDate, e.ExDate = master.UID, &rid, "", nil, nil
		s.Overrides = append(s.Overrides, e)
	}
	return s, nil
}

func node(in Todo, tz string, top bool) (transfer.TodoNode, error) {
	if strings.TrimSpace(in.Title) == "" {
		return transfer.TodoNode{}, errors.New("has no title")
	}
	if in.Timezone != "" && domain.ValidTimezone(in.Timezone) {
		tz = in.Timezone
	}
	t := domain.Todo{
		UID: in.UID, Title: in.Title, Body: in.Body, Status: domain.TodoOpen, Priority: in.Priority,
		TZ: tz, Reminders: in.Reminders,
	}
	if in.Priority < 0 || in.Priority > domain.MaxTodoPriority {
		t.Priority = 0
	}
	if in.Due != "" {
		d, err := time.Parse(domain.DateLayout, strings.TrimSpace(in.Due))
		if err != nil {
			return transfer.TodoNode{}, fmt.Errorf("due %q is not a date", in.Due)
		}
		t.DueDate = &d
		if in.Time != "" {
			m, ok := domain.ParseClock(in.Time)
			if !ok {
				return transfer.TodoNode{}, fmt.Errorf("time %q is not a time", in.Time)
			}
			t.DueTime, t.ShowOnCalendar = &m, true
		}
		if in.Duration != "" && t.DueTime != nil {
			d, err := time.ParseDuration(in.Duration)
			if err != nil || d < time.Minute || d > 24*time.Hour {
				return transfer.TodoNode{}, fmt.Errorf("duration %q must be between 1m and 24h", in.Duration)
			}
			t.Duration = int(d / time.Minute)
		}
		if in.RRule != "" {
			n, err := recurrence.Normalize(in.RRule, t.Zone())
			if err != nil {
				return transfer.TodoNode{}, fmt.Errorf("rrule %w", err)
			}
			t.RRule = n
		}
	}
	if in.ShowOnCalendar != nil {
		t.ShowOnCalendar = *in.ShowOnCalendar
	}
	if in.Status == domain.TodoCompleted || in.CompletedAt != "" {
		at := time.Now().UTC()
		if in.CompletedAt != "" {
			if parsed, err := time.Parse(time.RFC3339, in.CompletedAt); err == nil {
				at = parsed.UTC()
			}
		}
		t.Status, t.CompletedAt = domain.TodoCompleted, &at
	}
	for _, c := range in.Checks {
		if text := strings.TrimSpace(c.Text); text != "" && len(t.Checks) < domain.MaxChecks {
			t.Checks = append(t.Checks, domain.Check{Text: transfer.Clip(text, 500), Done: c.Done})
		}
	}
	n := transfer.TodoNode{Todo: t}
	if !top {
		return n, nil
	}
	for _, sub := range in.Subtasks {
		s, err := node(sub, tz, false)
		if err != nil {
			return n, fmt.Errorf("subtask: %w", err)
		}
		n.Subtasks = append(n.Subtasks, s)
	}
	return n, nil
}

func Encode(set transfer.Set, u *domain.User) ([]byte, error) {
	f := File{Format: Format, Schema: Schema, ExportedAt: time.Now().UTC().Format(time.RFC3339)}
	if u != nil {
		f.Settings = &Settings{
			Timezone: u.Timezone, TimeFormat: u.TimeFormat, DefaultView: u.DefaultView,
			WeekStart:        weekdays[min(max(u.WeekStart, 0), 6)],
			DateOnlyReminder: domain.FormatClock(u.DateOnlyReminder),
		}
	}
	for _, c := range set.Calendars {
		out := Calendar{Name: c.Name, Color: c.Color, Timezone: c.Timezone, Description: c.Description, Hidden: c.Hidden, Reminders: c.Reminders}
		for _, s := range c.Series {
			ev := encodeEvent(s.Master)
			for _, ov := range s.Overrides {
				o := Override{RecurrenceID: stamp(*ov.RecurrenceID, ov), Event: encodeEvent(ov)}
				o.UID, o.AllDay, o.Timezone = "", false, ""
				ev.Overrides = append(ev.Overrides, o)
			}
			out.Events = append(out.Events, ev)
		}
		f.Calendars = append(f.Calendars, out)
	}
	for _, l := range set.Lists {
		out := List{Name: l.Name, Color: l.Color}
		for _, n := range l.Todos {
			td := encodeTodo(n.Todo)
			for _, sub := range n.Subtasks {
				td.Subtasks = append(td.Subtasks, encodeTodo(sub.Todo))
			}
			out.Todos = append(out.Todos, td)
		}
		f.TodoLists = append(f.TodoLists, out)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(f); err != nil {
		return nil, err
	}
	return buf.Bytes(), enc.Close()
}

func stamp(t time.Time, e domain.Event) string {
	if e.AllDay {
		return t.UTC().Format(domain.DateLayout)
	}
	return t.In(e.Zone()).Format(time.RFC3339)
}

func encodeEvent(e domain.Event) Event {
	out := Event{
		UID: e.UID, Title: e.Title, Body: e.Body, Location: e.Location, URL: e.URL, AllDay: e.AllDay,
		Start: stamp(e.Start, e), End: stamp(e.End, e), Timezone: e.TZ, RRule: e.RRule, Color: e.Color, Reminders: e.Reminders,
	}
	if e.Status != domain.StatusConfirmed {
		out.Status = e.Status
	}
	if e.Transparency != domain.TransparencyOpaque {
		out.Transparency = e.Transparency
	}
	if e.Visibility != domain.VisibilityDefault {
		out.Visibility = e.Visibility
	}
	for _, t := range e.RDate {
		out.RDates = append(out.RDates, stamp(t, e))
	}
	for _, t := range e.ExDate {
		out.ExDates = append(out.ExDates, stamp(t, e))
	}
	return out
}

func encodeTodo(t domain.Todo) Todo {
	out := Todo{
		UID: t.UID, Title: t.Title, Body: t.Body, Priority: t.Priority, Timezone: t.TZ, RRule: t.RRule,
		Reminders: t.Reminders,
	}
	show := t.ShowOnCalendar
	out.ShowOnCalendar = &show
	if t.DueDate != nil {
		out.Due = t.DueDate.Format(domain.DateLayout)
	}
	if t.DueTime != nil {
		out.Time = domain.FormatClock(*t.DueTime)
	}
	if t.Duration > 0 {
		out.Duration = (time.Duration(t.Duration) * time.Minute).String()
	}
	if t.Done() {
		out.Status = domain.TodoCompleted
		if t.CompletedAt != nil {
			out.CompletedAt = t.CompletedAt.UTC().Format(time.RFC3339)
		}
	}
	for _, c := range t.Checks {
		out.Checks = append(out.Checks, Check{Text: c.Text, Done: c.Done})
	}
	return out
}

const (
	maxNodes = 100_000
	maxDepth = 32
)

// measure walks the tree the way the decoder will, following aliases so that a
// small file of anchors cannot expand into millions of nodes.
func measure(n *yaml.Node) error {
	count := 0
	var walk func(*yaml.Node, int) error
	walk = func(n *yaml.Node, depth int) error {
		if n == nil {
			return nil
		}
		if depth > maxDepth {
			return fmt.Errorf("%w: the file nests deeper than %d levels", domain.ErrInvalid, maxDepth)
		}
		count++
		if count > maxNodes {
			return fmt.Errorf("%w: the file expands to more than %d nodes", domain.ErrInvalid, maxNodes)
		}
		if n.Alias != nil {
			return walk(n.Alias, depth+1)
		}
		for _, c := range n.Content {
			if err := walk(c, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(n, 0)
}
