package ical

import (
	"bytes"
	"strconv"
	"strings"
	"time"

	goical "github.com/emersion/go-ical"

	"github.com/Alexander-D-Karpov/calendar/internal/buildinfo"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/transfer"
)

func ProductID() string {
	return "-//Alexander-D-Karpov//calendar " + buildinfo.Get().Version + "//EN"
}

func Encode(set transfer.Set, name string) ([]byte, error) {
	cal := goical.NewCalendar()
	cal.Props.SetText(goical.PropProductID, ProductID())
	cal.Props.SetText(goical.PropVersion, "2.0")
	cal.Props.SetText("CALSCALE", "GREGORIAN")
	cal.Props.SetText("METHOD", "PUBLISH")
	if name != "" {
		cal.Props.SetText("X-WR-CALNAME", name)
	}
	stamp := time.Now().UTC()
	for _, c := range set.Calendars {
		for _, s := range c.Series {
			cal.Children = append(cal.Children, encodeEvent(s.Master, stamp))
			for _, ov := range s.Overrides {
				cal.Children = append(cal.Children, encodeEvent(ov, stamp))
			}
		}
	}
	for _, l := range set.Lists {
		for _, n := range l.Todos {
			cal.Children = append(cal.Children, encodeTodo(n.Todo, "", stamp))
			for _, sub := range n.Subtasks {
				cal.Children = append(cal.Children, encodeTodo(sub.Todo, n.Todo.UID, stamp))
			}
		}
	}
	var buf bytes.Buffer
	if err := goical.NewEncoder(&buf).Encode(cal); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func stampProps(props goical.Props, uid string, at time.Time) {
	props.SetText(goical.PropUID, uid)
	props.SetDateTime(goical.PropDateTimeStamp, at)
}

func setWhen(props goical.Props, name string, t time.Time, e domain.Event) {
	if e.AllDay {
		p := &goical.Prop{Name: name, Value: t.UTC().Format("20060102"), Params: goical.Params{}}
		p.Params.Set(goical.ParamValue, "DATE")
		props.Set(p)
		return
	}
	z := e.Zone()
	p := &goical.Prop{Name: name, Value: t.In(z).Format("20060102T150405"), Params: goical.Params{}}
	if e.TZ == "" || e.TZ == "UTC" {
		p.Value = t.UTC().Format("20060102T150405Z")
	} else {
		p.Params.Set(goical.ParamTimezoneID, e.TZ)
	}
	props.Set(p)
}

func setDates(props goical.Props, name string, ts []time.Time, e domain.Event) {
	if len(ts) == 0 {
		return
	}
	parts := make([]string, len(ts))
	p := &goical.Prop{Name: name, Params: goical.Params{}}
	switch {
	case e.AllDay:
		for i, t := range ts {
			parts[i] = t.UTC().Format("20060102")
		}
		p.Params.Set(goical.ParamValue, "DATE")
	case e.TZ == "" || e.TZ == "UTC":
		for i, t := range ts {
			parts[i] = t.UTC().Format("20060102T150405Z")
		}
	default:
		z := e.Zone()
		for i, t := range ts {
			parts[i] = t.In(z).Format("20060102T150405")
		}
		p.Params.Set(goical.ParamTimezoneID, e.TZ)
	}
	p.Value = strings.Join(parts, ",")
	props.Set(p)
}

func encodeEvent(e domain.Event, stamp time.Time) *goical.Component {
	c := goical.NewComponent(goical.CompEvent)
	stampProps(c.Props, e.UID, stamp)
	if !e.CreatedAt.IsZero() {
		c.Props.SetDateTime("CREATED", e.CreatedAt.UTC())
	}
	if !e.UpdatedAt.IsZero() {
		c.Props.SetDateTime("LAST-MODIFIED", e.UpdatedAt.UTC())
	}
	c.Props.SetText(goical.PropSummary, e.Title)
	if e.Body != "" {
		c.Props.SetText(goical.PropDescription, e.Body)
	}
	if e.Location != "" {
		c.Props.SetText(goical.PropLocation, e.Location)
	}
	if e.URL != "" {
		c.Props.Set(&goical.Prop{Name: goical.PropURL, Value: e.URL})
	}
	setWhen(c.Props, goical.PropDateTimeStart, e.Start, e)
	setWhen(c.Props, goical.PropDateTimeEnd, e.End, e)
	if e.RRule != "" {
		c.Props.Set(&goical.Prop{Name: goical.PropRecurrenceRule, Value: e.RRule})
	}
	setDates(c.Props, "RDATE", e.RDate, e)
	setDates(c.Props, goical.PropExceptionDates, e.ExDate, e)
	if e.RecurrenceID != nil {
		setWhen(c.Props, "RECURRENCE-ID", *e.RecurrenceID, e)
	}
	c.Props.SetText(goical.PropStatus, strings.ToUpper(e.Status))
	if e.Transparency == domain.TransparencyTransparent {
		c.Props.SetText("TRANSP", "TRANSPARENT")
	}
	if e.Visibility == domain.VisibilityPrivate {
		c.Props.SetText("CLASS", "PRIVATE")
	}
	if e.Sequence > 0 {
		c.Props.SetText("SEQUENCE", strconv.Itoa(e.Sequence))
	}
	addAlarms(c, e.Reminders, e.Title)
	return c
}

func encodeTodo(t domain.Todo, parent string, stamp time.Time) *goical.Component {
	c := goical.NewComponent(goical.CompToDo)
	uid := t.UID
	if uid == "" {
		uid = t.ID.String()
	}
	stampProps(c.Props, uid, stamp)
	if !t.CreatedAt.IsZero() {
		c.Props.SetDateTime("CREATED", t.CreatedAt.UTC())
	}
	if !t.UpdatedAt.IsZero() {
		c.Props.SetDateTime("LAST-MODIFIED", t.UpdatedAt.UTC())
	}
	c.Props.SetText(goical.PropSummary, t.Title)
	if body := todoNotes(t); body != "" {
		c.Props.SetText(goical.PropDescription, body)
	}
	if parent != "" {
		c.Props.SetText("RELATED-TO", parent)
	}
	if t.DueDate != nil {
		e := domain.Event{AllDay: t.DueTime == nil, TZ: t.TZ}
		at, _ := t.DueStart()
		setWhen(c.Props, "DUE", at, e)
		if t.RRule != "" {
			c.Props.Set(&goical.Prop{Name: goical.PropRecurrenceRule, Value: t.RRule})
		}
	}
	if t.Done() {
		c.Props.SetText(goical.PropStatus, "COMPLETED")
		c.Props.SetText("PERCENT-COMPLETE", "100")
		if t.CompletedAt != nil {
			c.Props.SetDateTime("COMPLETED", t.CompletedAt.UTC())
		}
	} else {
		c.Props.SetText(goical.PropStatus, "NEEDS-ACTION")
	}
	if p := map[int]int{1: 9, 2: 5, 3: 1}[t.Priority]; p > 0 {
		c.Props.SetText("PRIORITY", strconv.Itoa(p))
	}
	addAlarms(c, t.Reminders, t.Title)
	return c
}

func todoNotes(t domain.Todo) string {
	if len(t.Checks) == 0 {
		return t.Body
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(t.Body, " \t\n"))
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
	b.WriteString("---")
	for _, c := range t.Checks {
		mark := " "
		if c.Done {
			mark = "x"
		}
		b.WriteString("\n[" + mark + "] " + c.Text)
	}
	return b.String()
}

func addAlarms(c *goical.Component, reminders []int, title string) {
	for _, m := range reminders {
		a := goical.NewComponent(goical.CompAlarm)
		a.Props.SetText(goical.PropAction, "DISPLAY")
		a.Props.SetText(goical.PropDescription, title)
		p := &goical.Prop{Name: goical.PropTrigger, Value: "-PT" + strconv.Itoa(m) + "M", Params: goical.Params{}}
		p.Params.Set(goical.ParamValue, "DURATION")
		a.Props.Set(p)
		c.Children = append(c.Children, a)
	}
}
