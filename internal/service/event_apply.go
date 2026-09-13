package service

import (
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/recurrence"
)

const (
	maxRDates  = 1000
	maxExDates = 5000
	maxRange   = 400 * 24 * time.Hour
)

var localLayouts = []string{"2006-01-02T15:04:05", "2006-01-02T15:04"}

func parseDateTime(s string, loc *time.Location) (time.Time, bool) {
	s = strings.ReplaceAll(strings.TrimSpace(s), " ", "+")
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), true
	}
	for _, l := range localLayouts {
		if t, err := time.ParseInLocation(l, s, loc); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func parseDate(s string) (time.Time, bool) {
	t, err := time.Parse(domain.DateLayout, strings.TrimSpace(s))
	return t, err == nil
}

func parseEventTime(s string, allDay bool, loc *time.Location) (time.Time, bool) {
	if allDay {
		return parseDate(s)
	}
	return parseDateTime(s, loc)
}

func parseInstant(s string, loc *time.Location) (time.Time, bool) {
	if t, ok := parseDateTime(s, loc); ok {
		return t, true
	}
	if d, ok := parseDate(s); ok {
		return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc).UTC(), true
	}
	return time.Time{}, false
}

func timeFormatHint(allDay bool) string {
	if allDay {
		return "must be a date like 2026-09-20"
	}
	return "must be a date-time like 2026-09-10T10:00:00+03:00"
}

func applyEvent(e *domain.Event, p domain.EventPatch, isNew bool) error {
	var v domain.ValidationError
	before := *e

	if p.UID.Set {
		if !isNew {
			v.Add("uid", "cannot be changed")
		} else if x, ok := p.UID.Value(&v, "uid"); ok {
			e.UID = strings.TrimSpace(x)
		}
	}
	setText(p.Title, &e.Title, true)
	setText(p.Body, &e.Body, false)
	setText(p.Location, &e.Location, true)
	setText(p.URL, &e.URL, true)

	if x, ok := p.AllDay.Value(&v, "all_day"); ok {
		e.AllDay = x
	}
	switched := e.AllDay != before.AllDay
	applyTimezone(&v, e, p.Timezone)
	applyTiming(&v, e, before, p, isNew || switched)
	applyRecurrence(&v, e, p, switched)
	applyChoices(&v, e, p)
	validateEvent(&v, e, p.UID.Set)

	if !isNew && !sameTiming(before, *e) {
		e.Sequence++
	}
	return v.Err()
}

func setText(o domain.Opt[string], dst *string, trim bool) {
	if !o.Set {
		return
	}
	s := o.V
	if trim {
		s = strings.TrimSpace(s)
	}
	*dst = s
}

func applyTimezone(v *domain.ValidationError, e *domain.Event, o domain.Opt[string]) {
	if !o.Set {
		return
	}
	tz := strings.TrimSpace(o.V)
	switch {
	case o.Null || tz == "":
		if !e.AllDay {
			v.Add("timezone", "is required for timed events")
		}
	case domain.ValidTimezone(tz):
		e.TZ = tz
	default:
		v.Add("timezone", "unknown timezone")
	}
}

func applyTiming(v *domain.ValidationError, e *domain.Event, before domain.Event, p domain.EventPatch, needStart bool) {
	loc := e.Zone()
	startSet := false
	switch {
	case p.Start.Set && !p.Start.Null:
		t, ok := parseEventTime(p.Start.V, e.AllDay, loc)
		if !ok {
			v.Add("start", timeFormatHint(e.AllDay))
			return
		}
		e.Start, startSet = t, true
	case p.Start.Set:
		v.Add("start", "must not be null")
		return
	case needStart:
		v.Add("start", "is required")
		return
	}
	switch {
	case p.End.Set && !p.End.Null:
		t, ok := parseEventTime(p.End.V, e.AllDay, loc)
		if !ok {
			v.Add("end", timeFormatHint(e.AllDay))
			return
		}
		e.End = t
	case needStart || (p.End.Set && p.End.Null):
		e.End = e.Start.Add(defaultDuration(e.AllDay))
	case startSet:
		e.End = e.Start.Add(before.Duration())
	}
}

func defaultDuration(allDay bool) time.Duration {
	if allDay {
		return 24 * time.Hour
	}
	return time.Hour
}

func applyRecurrence(v *domain.ValidationError, e *domain.Event, p domain.EventPatch, switched bool) {
	if switched {
		if !p.RDate.Set {
			e.RDate = nil
		}
		if !p.ExDate.Set {
			e.ExDate = nil
		}
	}
	if e.IsOverride() {
		if (p.RRule.Set && p.RRule.V != "") || (p.RDate.Set && len(p.RDate.V) > 0) || (p.ExDate.Set && len(p.ExDate.V) > 0) {
			v.Add("rrule", "only the whole series can change recurrence")
		}
		return
	}
	if p.RRule.Set {
		rule := strings.TrimSpace(p.RRule.V)
		if p.RRule.Null || rule == "" {
			e.RRule = ""
		} else if norm, err := recurrence.Normalize(rule, e.Zone()); err != nil {
			v.Add("rrule", err.Error())
		} else {
			e.RRule = norm
		}
	}
	e.RDate = applyDates(v, "rdate", p.RDate, e.RDate, e, maxRDates)
	e.ExDate = applyDates(v, "exdate", p.ExDate, e.ExDate, e, maxExDates)
}

func applyDates(v *domain.ValidationError, field string, o domain.Opt[[]string], cur []time.Time, e *domain.Event, limit int) []time.Time {
	if !o.Set {
		return cur
	}
	if o.Null || len(o.V) == 0 {
		return nil
	}
	if len(o.V) > limit {
		v.Addf(field, "must have at most %d entries", limit)
		return cur
	}
	out := make([]time.Time, 0, len(o.V))
	for _, s := range o.V {
		t, ok := parseEventTime(s, e.AllDay, e.Zone())
		if !ok {
			v.Add(field, timeFormatHint(e.AllDay))
			return cur
		}
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b time.Time) int { return a.Compare(b) })
	return slices.CompactFunc(out, func(a, b time.Time) bool { return a.Equal(b) })
}

func applyChoices(v *domain.ValidationError, e *domain.Event, p domain.EventPatch) {
	choice(v, p.Status, "status", &e.Status, domain.StatusConfirmed, domain.StatusTentative, domain.StatusCancelled)
	choice(v, p.Transparency, "transparency", &e.Transparency, domain.TransparencyOpaque, domain.TransparencyTransparent)
	choice(v, p.Visibility, "visibility", &e.Visibility, domain.VisibilityDefault, domain.VisibilityPrivate)
	if p.Color.Set {
		if p.Color.Null || strings.TrimSpace(p.Color.V) == "" {
			e.Color = ""
		} else if c, ok := domain.NormalizeColor(p.Color.V); ok {
			e.Color = c
		} else {
			v.Add("color", "must be a hex color like #3b6ea5")
		}
	}
	if p.Reminders.Set {
		e.Reminders = domain.NormalizeReminders(v, "reminders", p.Reminders.V)
	}
}

func choice(v *domain.ValidationError, o domain.Opt[string], field string, dst *string, allowed ...string) {
	x, ok := o.Value(v, field)
	if !ok {
		return
	}
	if !slices.Contains(allowed, x) {
		v.Addf(field, "must be one of %s", strings.Join(allowed, ", "))
		return
	}
	*dst = x
}

func validateEvent(v *domain.ValidationError, e *domain.Event, uidSet bool) {
	if uidSet && (utf8.RuneCountInString(e.UID) < 1 || len(e.UID) > 1000 || strings.IndexFunc(e.UID, unicode.IsControl) >= 0) {
		v.Add("uid", "must be 1 to 1000 printable characters")
	}
	v.Length("title", e.Title, 0, 1000)
	v.Length("body", e.Body, 0, 100000)
	v.Length("location", e.Location, 0, 1000)
	if e.URL != "" && (len(e.URL) > 2048 || !validURL(e.URL)) {
		v.Add("url", "must be an http or https URL of at most 2048 characters")
	}
	if !e.AllDay && e.TZ == "" {
		v.Add("timezone", "is required for timed events")
	}
	if hasField(v, "start") || hasField(v, "end") {
		return
	}
	switch {
	case e.AllDay && !e.End.After(e.Start):
		v.Add("end", "must be after start")
	case !e.AllDay && e.End.Before(e.Start):
		v.Add("end", "must not be before start")
	case e.End.Sub(e.Start) > domain.MaxEventDuration:
		v.Add("end", "event must not be longer than 366 days")
	}
}

func validURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && !strings.ContainsAny(s, " \t\r\n")
}

func hasField(v *domain.ValidationError, field string) bool {
	return slices.ContainsFunc(v.Fields, func(f domain.FieldError) bool { return f.Field == field })
}

func sameTiming(a, b domain.Event) bool {
	eq := func(x, y time.Time) bool { return x.Equal(y) }
	return a.Start.Equal(b.Start) && a.End.Equal(b.End) && a.AllDay == b.AllDay &&
		(a.AllDay || a.TZ == b.TZ) && a.RRule == b.RRule &&
		slices.EqualFunc(a.RDate, b.RDate, eq) && slices.EqualFunc(a.ExDate, b.ExDate, eq)
}

func parseRange(v *domain.ValidationError, from, to string, loc *time.Location) (time.Time, time.Time) {
	f, fok := parseInstant(from, loc)
	t, tok := parseInstant(to, loc)
	check := func(field, raw string, ok bool) {
		switch {
		case strings.TrimSpace(raw) == "":
			v.Add(field, "is required")
		case !ok:
			v.Add(field, "must be a date-time like 2026-09-07T00:00:00+03:00 or a date like 2026-09-07")
		}
	}
	check("from", from, fok)
	check("to", to, tok)
	if fok && tok {
		switch {
		case !t.After(f):
			v.Add("to", "must be after from")
		case t.Sub(f) > maxRange:
			v.Add("to", "range must be at most 400 days")
		}
	}
	return f, t
}
