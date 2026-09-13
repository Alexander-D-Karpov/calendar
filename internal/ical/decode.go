package ical

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	goical "github.com/emersion/go-ical"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/recurrence"
	"github.com/Alexander-D-Karpov/calendar/internal/transfer"
)

var errDuration = errors.New("invalid duration")

type Options struct {
	Timezone string
	MaxItems int
}

type rawEvent struct {
	ev       domain.Event
	rid      *time.Time
	modified time.Time
	where    string
}

type rawTodo struct {
	node   transfer.TodoNode
	parent string
}

func Decode(data []byte, o Options) (transfer.Set, error) {
	var set transfer.Set
	dec := goical.NewDecoder(bytes.NewReader(data))
	count, found := 0, false
	for {
		cal, err := dec.Decode()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return set, fmt.Errorf("%w: the file is not valid iCalendar: %w", domain.ErrInvalid, err)
		}
		found = true
		if err := decodeCalendar(&set, cal, o, &count); err != nil {
			return set, err
		}
	}
	if !found {
		return set, fmt.Errorf("%w: the file has no calendar", domain.ErrInvalid)
	}
	return set, nil
}

func decodeCalendar(set *transfer.Set, cal *goical.Calendar, o Options, count *int) error {
	tz := o.Timezone
	calTZ, ok := transfer.Zone(value(cal.Component, "X-WR-TIMEZONE"))
	if ok {
		tz = calTZ
	}
	name := text(cal.Props.Get("X-WR-CALNAME"))
	var events []rawEvent
	var todos []rawTodo
	for i, c := range cal.Children {
		if c.Name != "VEVENT" && c.Name != "VTODO" {
			continue
		}
		*count++
		if o.MaxItems > 0 && *count > o.MaxItems {
			return fmt.Errorf("%w: the file has more than %d entries", domain.ErrInvalid, o.MaxItems)
		}
		where := label(c, i)
		if c.Name == "VTODO" {
			n, parent := decodeTodo(c, tz)
			n.Where = where
			todos = append(todos, rawTodo{node: n, parent: parent})
			continue
		}
		ev, rid, mod, err := decodeEvent(c, tz)
		if err != nil {
			set.Problem(where, "%s", err.Error())
			continue
		}
		events = append(events, rawEvent{ev: ev, rid: rid, modified: mod, where: where})
	}
	set.Calendars = append(set.Calendars, transfer.Calendar{Name: name, Timezone: calTZ, Series: assemble(set, events)})
	if len(todos) > 0 {
		set.Lists = append(set.Lists, transfer.List{Name: name, Todos: tree(todos)})
	}
	return nil
}

func label(c *goical.Component, i int) string {
	kind := "event"
	if c.Name == "VTODO" {
		kind = "todo"
	}
	if t := text(c.Props.Get("SUMMARY")); t != "" {
		return kind + " " + strconv.Quote(transfer.Clip(t, 60))
	}
	return fmt.Sprintf("%s %d", kind, i+1)
}

func assemble(set *transfer.Set, events []rawEvent) []transfer.Series {
	var out []transfer.Series
	byUID := map[string]int{}
	for _, r := range events {
		if r.rid != nil || r.ev.Status == domain.StatusCancelled {
			continue
		}
		if r.ev.UID != "" {
			if i, dup := byUID[r.ev.UID]; dup {
				cur := out[i]
				if r.ev.Sequence > cur.Master.Sequence || (r.ev.Sequence == cur.Master.Sequence && r.modified.After(cur.Modified)) {
					out[i].Master, out[i].Modified, out[i].Where = r.ev, r.modified, r.where
				}
				continue
			}
			byUID[r.ev.UID] = len(out)
		}
		out = append(out, transfer.Series{Master: r.ev, Modified: r.modified, Where: r.where})
	}
	for _, r := range events {
		if r.rid == nil {
			continue
		}
		i, ok := byUID[r.ev.UID]
		if r.ev.UID == "" || !ok {
			if r.ev.Status != domain.StatusCancelled {
				set.Problem(r.where, "is a changed occurrence without its series")
			}
			continue
		}
		if r.ev.Status == domain.StatusCancelled {
			out[i].Master.ExDate = append(out[i].Master.ExDate, *r.rid)
			continue
		}
		ev := r.ev
		ev.RecurrenceID = r.rid
		out[i].Overrides = append(out[i].Overrides, ev)
		if r.modified.After(out[i].Modified) {
			out[i].Modified = r.modified
		}
	}
	for i := range out {
		out[i].Master.ExDate = sortTimes(out[i].Master.ExDate)
	}
	return out
}

func decodeEvent(c *goical.Component, tz string) (domain.Event, *time.Time, time.Time, error) {
	e := domain.Event{
		UID:          strings.TrimSpace(value(c, "UID")),
		Title:        text(c.Props.Get("SUMMARY")),
		Body:         text(c.Props.Get("DESCRIPTION")),
		Location:     text(c.Props.Get("LOCATION")),
		URL:          strings.TrimSpace(value(c, "URL")),
		Status:       domain.StatusConfirmed,
		Transparency: domain.TransparencyOpaque,
		Visibility:   domain.VisibilityDefault,
	}
	mod := modified(c)
	start, allDay, zone, err := propTime(c.Props.Get("DTSTART"), tz)
	if err != nil {
		return e, nil, mod, errors.New("has no valid start")
	}
	e.Start, e.AllDay = start, allDay
	if !allDay {
		e.TZ = zone
	}
	if p := c.Props.Get("DTEND"); p != nil {
		if end, _, _, err := propTime(p, zone); err == nil {
			e.End = end
		}
	} else if d, err := parseDuration(value(c, "DURATION")); err == nil && d >= 0 {
		e.End = start.Add(d)
	}
	switch strings.ToUpper(value(c, "STATUS")) {
	case "TENTATIVE":
		e.Status = domain.StatusTentative
	case "CANCELLED":
		e.Status = domain.StatusCancelled
	}
	if strings.EqualFold(value(c, "TRANSP"), "TRANSPARENT") {
		e.Transparency = domain.TransparencyTransparent
	}
	switch strings.ToUpper(value(c, "CLASS")) {
	case "PRIVATE", "CONFIDENTIAL":
		e.Visibility = domain.VisibilityPrivate
	}
	if n, err := strconv.Atoi(value(c, "SEQUENCE")); err == nil {
		e.Sequence = min(max(n, 0), domain.MaxSequence)
	}
	e.Reminders = alarms(c)
	if rule := value(c, "RRULE"); rule != "" {
		n, err := recurrence.Normalize(rule, zoneLoc(zone, allDay))
		if err != nil {
			return e, nil, mod, fmt.Errorf("repeats in a way that is not supported: %w", err)
		}
		e.RRule = n
	}
	e.RDate = dates(c.Props["RDATE"], zone, allDay)
	e.ExDate = dates(c.Props["EXDATE"], zone, allDay)
	var rid *time.Time
	if p := c.Props.Get("RECURRENCE-ID"); p != nil {
		t, _, _, err := propTime(p, zone)
		if err != nil {
			return e, nil, mod, errors.New("has an invalid RECURRENCE-ID")
		}
		if allDay {
			t = day(t)
		}
		rid = &t
	}
	return e, rid, mod, nil
}

func decodeTodo(c *goical.Component, tz string) (transfer.TodoNode, string) {
	mod := modified(c)
	t := domain.Todo{
		UID:    strings.TrimSpace(value(c, "UID")),
		Title:  text(c.Props.Get("SUMMARY")),
		Body:   text(c.Props.Get("DESCRIPTION")),
		Status: domain.TodoOpen,
		TZ:     tz,
	}
	status := strings.ToUpper(value(c, "STATUS"))
	if p := c.Props.Get("COMPLETED"); p != nil || status == "COMPLETED" || status == "CANCELLED" {
		at := mod
		if p != nil {
			if ct, _, _, err := propTime(p, "UTC"); err == nil {
				at = ct
			}
		}
		if at.IsZero() {
			at = time.Now().UTC()
		}
		t.Status, t.CompletedAt = domain.TodoCompleted, &at
	}
	switch n, _ := strconv.Atoi(value(c, "PRIORITY")); {
	case n >= 1 && n <= 4:
		t.Priority = 3
	case n == 5:
		t.Priority = 2
	case n >= 6 && n <= 9:
		t.Priority = 1
	}
	due := c.Props.Get("DUE")
	if due == nil {
		due = c.Props.Get("DTSTART")
	}
	if due != nil {
		if at, allDay, zone, err := propTime(due, tz); err == nil {
			lt := at.In(zoneLoc(zone, allDay))
			d := day(lt)
			t.DueDate = &d
			if !allDay {
				mins := lt.Hour()*60 + lt.Minute()
				t.DueTime, t.TZ, t.ShowOnCalendar = &mins, zone, true
			}
		}
	}
	if rule := value(c, "RRULE"); rule != "" && t.DueDate != nil {
		if n, err := recurrence.Normalize(rule, zoneLoc(t.TZ, t.DueTime == nil)); err == nil {
			t.RRule = n
		}
	}
	t.Reminders = alarms(c)
	return transfer.TodoNode{Todo: t, Modified: mod}, strings.TrimSpace(value(c, "RELATED-TO"))
}

func tree(items []rawTodo) []transfer.TodoNode {
	index := make(map[string]int, len(items))
	for i, r := range items {
		if r.node.Todo.UID != "" {
			if _, dup := index[r.node.Todo.UID]; !dup {
				index[r.node.Todo.UID] = i
			}
		}
	}
	child := make([]bool, len(items))
	for i, r := range items {
		p, ok := index[r.parent]
		if r.parent == "" || !ok || p == i || items[p].parent != "" {
			continue
		}
		items[p].node.Subtasks = append(items[p].node.Subtasks, r.node)
		child[i] = true
	}
	var out []transfer.TodoNode
	for i, r := range items {
		if !child[i] {
			out = append(out, r.node)
		}
	}
	return out
}

func value(c *goical.Component, name string) string {
	p := c.Props.Get(name)
	if p == nil {
		return ""
	}
	return p.Value
}

func text(p *goical.Prop) string {
	if p == nil {
		return ""
	}
	s, err := p.Text()
	if err != nil {
		s = p.Value
	}
	return strings.ReplaceAll(strings.TrimSpace(s), "\r\n", "\n")
}

func zoneLoc(zone string, allDay bool) *time.Location {
	if allDay || zone == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.UTC
	}
	return loc
}

func day(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func propTime(p *goical.Prop, fallback string) (time.Time, bool, string, error) {
	if p == nil {
		return time.Time{}, false, fallback, errors.New("missing")
	}
	zone := fallback
	if tz, ok := transfer.Zone(p.Params.Get(goical.ParamTimezoneID)); ok {
		zone = tz
	}
	isDate := strings.EqualFold(p.Params.Get(goical.ParamValue), "DATE")
	t, allDay, err := transfer.ParseICSTime(strings.TrimSpace(p.Value), zone, isDate)
	if err != nil {
		return time.Time{}, allDay, zone, err
	}
	if allDay {
		return day(t), true, zone, nil
	}
	if strings.HasSuffix(p.Value, "Z") {
		zone = "UTC"
	}
	return t, false, zone, nil
}

func dates(props []goical.Prop, zone string, allDay bool) []time.Time {
	var out []time.Time
	for i := range props {
		p := &props[i]
		z := zone
		if tz, ok := transfer.Zone(p.Params.Get(goical.ParamTimezoneID)); ok {
			z = tz
		}
		isDate := allDay || strings.EqualFold(p.Params.Get(goical.ParamValue), "DATE")
		for _, v := range strings.Split(p.Value, ",") {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			t, gotDate, err := transfer.ParseICSTime(v, z, isDate)
			if err != nil {
				continue
			}
			if gotDate || allDay {
				t = day(t)
			}
			out = append(out, t)
		}
	}
	return sortTimes(out)
}

func sortTimes(ts []time.Time) []time.Time {
	slices.SortFunc(ts, func(a, b time.Time) int { return a.Compare(b) })
	return slices.CompactFunc(ts, func(a, b time.Time) bool { return a.Equal(b) })
}

func modified(c *goical.Component) time.Time {
	for _, name := range []string{"LAST-MODIFIED", "DTSTAMP", "CREATED"} {
		if t, _, _, err := propTime(c.Props.Get(name), "UTC"); err == nil {
			return t
		}
	}
	return time.Time{}
}

func alarms(c *goical.Component) []int {
	var out []int
	for _, a := range c.Children {
		if a.Name != "VALARM" || len(out) >= domain.MaxReminders {
			continue
		}
		p := a.Props.Get("TRIGGER")
		if p == nil || !strings.EqualFold(p.Params.Get(goical.ParamValue), "DURATION") && p.Params.Get(goical.ParamValue) != "" {
			continue
		}
		d, err := parseDuration(p.Value)
		if err != nil || d > 0 {
			continue
		}
		mins := int(-d / time.Minute)
		if mins <= domain.MaxReminderMinutes && !slices.Contains(out, mins) {
			out = append(out, mins)
		}
	}
	slices.Sort(out)
	return out
}

func parseDuration(s string) (time.Duration, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	sign := time.Duration(1)
	switch {
	case strings.HasPrefix(s, "-"):
		sign, s = -1, s[1:]
	case strings.HasPrefix(s, "+"):
		s = s[1:]
	}
	rest, ok := strings.CutPrefix(s, "P")
	if !ok || rest == "" {
		return 0, errDuration
	}
	units := map[byte]time.Duration{'W': 7 * 24 * time.Hour, 'D': 24 * time.Hour, 'H': time.Hour, 'M': time.Minute, 'S': time.Second}
	var total time.Duration
	num := ""
	inTime := false
	for i := 0; i < len(rest); i++ {
		ch := rest[i]
		switch {
		case ch == 'T':
			inTime, num = true, ""
		case ch >= '0' && ch <= '9':
			num += string(ch)
		default:
			unit, ok := units[ch]
			if !ok || num == "" {
				return 0, errDuration
			}
			if ch == 'M' && !inTime {
				return 0, errDuration
			}
			n, err := strconv.Atoi(num)
			if err != nil {
				return 0, errDuration
			}
			total, num = total+time.Duration(n)*unit, ""
		}
	}
	if num != "" {
		return 0, errDuration
	}
	return sign * total, nil
}
