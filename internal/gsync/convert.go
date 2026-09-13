package gsync

import (
	"slices"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/google"
	"github.com/Alexander-D-Karpov/calendar/internal/recurrence"
)

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func validTZ(names ...string) string {
	for _, n := range names {
		if domain.ValidTimezone(n) {
			return n
		}
	}
	return "UTC"
}

func remoteTime(w google.When) (time.Time, bool, bool) {
	if w.Date != "" {
		d, err := time.Parse(domain.DateLayout, w.Date)
		return d, true, err == nil
	}
	if w.DateTime != "" {
		t, err := time.Parse(time.RFC3339, w.DateTime)
		return t.UTC(), false, err == nil
	}
	return time.Time{}, false, false
}

func reminders(in []int) []int {
	out := slices.DeleteFunc(slices.Clone(in), func(m int) bool { return m < 0 || m > domain.MaxReminderMinutes })
	slices.Sort(out)
	out = slices.Compact(out)
	if len(out) > domain.MaxReminders {
		out = out[:domain.MaxReminders]
	}
	if out == nil {
		out = []int{}
	}
	return out
}

func eventFromRemote(re google.Event, base domain.Event, zones []string, defaults []int) (domain.Event, bool) {
	start, allDay, ok := remoteTime(re.Start)
	if !ok {
		return base, false
	}
	end, _, eok := remoteTime(re.End)
	e := base
	e.AllDay = allDay
	e.TZ = validTZ(zones...)
	switch {
	case !eok, allDay && !end.After(start), !allDay && end.Before(start):
		end = start.Add(time.Hour)
		if allDay {
			end = start.AddDate(0, 0, 1)
		}
	}
	e.Start, e.End = start, end
	e.Title = clip(re.Summary, 1000)
	e.Body = clip(google.DescriptionToMarkdown(re.Description), 100000)
	e.Location = clip(re.Location, 1000)
	e.Status = domain.StatusConfirmed
	if re.Status == google.StatusTentative {
		e.Status = domain.StatusTentative
	}
	e.Transparency = domain.TransparencyOpaque
	if re.Transparency == domain.TransparencyTransparent {
		e.Transparency = domain.TransparencyTransparent
	}
	e.Visibility = domain.VisibilityDefault
	if re.Visibility == "private" || re.Visibility == "confidential" {
		e.Visibility = domain.VisibilityPrivate
	}
	e.ReadOnly = !re.Editable
	if re.UseDefaultReminders {
		e.Reminders = reminders(defaults)
	} else {
		e.Reminders = reminders(re.Reminders)
	}
	e.RRule, e.RDate, e.ExDate = "", nil, nil
	if re.RecurringEventID == "" && len(re.Recurrence) > 0 {
		loc, err := time.LoadLocation(e.TZ)
		if err != nil {
			loc = time.UTC
		}
		var lossy bool
		e.RRule, e.RDate, e.ExDate, lossy = parseRecurrence(re.Recurrence, loc, allDay)
		e.ReadOnly = e.ReadOnly || lossy
	}
	return e, true
}

// parseRecurrence reports lossy when Google sent an RRULE that cannot be
// represented locally, so the caller can keep the event read-only instead of
// pushing back an edit that would drop the recurrence.
func parseRecurrence(lines []string, loc *time.Location, allDay bool) (rule string, rdate, exdate []time.Time, lossy bool) {
	for _, l := range lines {
		head, value, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		params := strings.Split(head, ";")
		zone := loc
		for _, p := range params[1:] {
			if tz, ok := strings.CutPrefix(strings.ToUpper(p), "TZID="); ok {
				if z, err := time.LoadLocation(p[len(p)-len(tz):]); err == nil {
					zone = z
				}
			}
		}
		switch strings.ToUpper(params[0]) {
		case "RRULE":
			if n, err := recurrence.Normalize(value, loc); err == nil {
				rule = n
			} else {
				lossy = true
			}
		case "EXDATE", "RDATE":
			var ts []time.Time
			for _, v := range strings.Split(value, ",") {
				if t, ok := parseICSTime(v, zone, allDay); ok {
					ts = append(ts, t)
				}
			}
			if strings.EqualFold(params[0], "EXDATE") {
				exdate = append(exdate, ts...)
			} else {
				rdate = append(rdate, ts...)
			}
		}
	}
	return rule, sortTimes(rdate), sortTimes(exdate), lossy
}

// sortTimes orders a RDATE or EXDATE list and drops repeats, so that a pull that
// re-adds an instance cannot grow the list forever.
func sortTimes(ts []time.Time) []time.Time {
	slices.SortFunc(ts, func(a, b time.Time) int { return a.Compare(b) })
	return slices.CompactFunc(ts, func(a, b time.Time) bool { return a.Equal(b) })
}

func parseICSTime(v string, loc *time.Location, allDay bool) (time.Time, bool) {
	v = strings.TrimSpace(v)
	var (
		t   time.Time
		err error
	)
	switch {
	case len(v) == 8:
		t, err = time.Parse("20060102", v)
		if err == nil && !allDay {
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
		}
	case strings.HasSuffix(v, "Z"):
		t, err = time.Parse("20060102T150405Z", v)
	default:
		t, err = time.ParseInLocation("20060102T150405", v, loc)
	}
	if err != nil {
		return time.Time{}, false
	}
	if allDay {
		y, m, d := t.Date()
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC), true
	}
	return t.UTC(), true
}

func formatRecurrence(e domain.Event) []string {
	var out []string
	if e.RRule != "" {
		out = append(out, "RRULE:"+e.RRule)
	}
	line := func(name string, ts []time.Time) string {
		parts := make([]string, len(ts))
		if e.AllDay {
			for i, t := range ts {
				parts[i] = t.UTC().Format("20060102")
			}
			return name + ";VALUE=DATE:" + strings.Join(parts, ",")
		}
		z := e.Zone()
		for i, t := range ts {
			parts[i] = t.In(z).Format("20060102T150405")
		}
		return name + ";TZID=" + e.TZ + ":" + strings.Join(parts, ",")
	}
	if len(e.ExDate) > 0 {
		out = append(out, line("EXDATE", e.ExDate))
	}
	if len(e.RDate) > 0 {
		out = append(out, line("RDATE", e.RDate))
	}
	return out
}

func toRemoteEvent(e domain.Event, instance bool) google.Event {
	re := google.Event{
		Summary:      e.Title,
		Description:  e.Body,
		Location:     e.Location,
		Reminders:    slices.Clone(e.Reminders),
		Transparency: e.Transparency,
		Visibility:   "default",
		Status:       google.StatusConfirmed,
	}
	switch e.Status {
	case domain.StatusTentative:
		re.Status = google.StatusTentative
	case domain.StatusCancelled:
		re.Status = google.StatusCancelled
	}
	if e.Visibility == domain.VisibilityPrivate {
		re.Visibility = "private"
	}
	if e.AllDay {
		re.Start = google.When{Date: e.Start.UTC().Format(domain.DateLayout)}
		re.End = google.When{Date: e.End.UTC().Format(domain.DateLayout)}
	} else {
		z := e.Zone()
		re.Start = google.When{DateTime: e.Start.In(z).Format(time.RFC3339), TimeZone: e.TZ}
		re.End = google.When{DateTime: e.End.In(z).Format(time.RFC3339), TimeZone: e.TZ}
	}
	if !instance {
		re.Recurrence, re.SendRecurrence = formatRecurrence(e), true
	}
	return re
}

func instanceStart(e domain.Event) string {
	if e.RecurrenceID == nil {
		return ""
	}
	if e.AllDay {
		return e.RecurrenceID.UTC().Format(domain.DateLayout)
	}
	return e.RecurrenceID.UTC().Format(time.RFC3339)
}

func todoDue(s string) *time.Time {
	if s == "" {
		return nil
	}
	d, err := time.Parse(domain.DateLayout, s)
	if err != nil {
		return nil
	}
	return &d
}

func sameDate(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Format(domain.DateLayout) == b.Format(domain.DateLayout)
}

func sameParent(a, b *domain.ID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func taskFromTodo(t domain.Todo, parent string) google.Task {
	checks := make([]google.Check, len(t.Checks))
	for i, c := range t.Checks {
		checks[i] = google.Check{Text: c.Text, Done: c.Done}
	}
	rt := google.Task{Title: t.Title, Notes: google.JoinNotes(t.Body, checks), Status: google.TaskOpen, Parent: parent}
	if t.DueDate != nil {
		rt.Due = t.DueDate.Format(domain.DateLayout)
	}
	if t.Done() {
		rt.Status, rt.Completed = google.TaskCompleted, t.CompletedAt
	}
	return rt
}

func sameChecks(have []domain.Check, want []google.Check) bool {
	return slices.EqualFunc(have, want, func(a domain.Check, b google.Check) bool {
		return a.Text == b.Text && a.Done == b.Done
	})
}
