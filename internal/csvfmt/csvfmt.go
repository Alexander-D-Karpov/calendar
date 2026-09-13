package csvfmt

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/transfer"
)

const bom = "\ufeff"

var (
	header = []string{"subject", "start date", "start time", "end date", "end time", "all day", "description",
		"location", "private", "free", "tentative", "reminders", "recurrence", "uid"}

	aliases = map[string][]string{
		"subject":     {"summary", "title", "name", "task", "event"},
		"start date":  {"start", "date", "begin date", "start_date"},
		"start time":  {"start_time", "time", "begin time"},
		"end date":    {"end", "end_date", "finish date", "due date", "due"},
		"end time":    {"end_time", "finish time", "due time"},
		"all day":     {"all day event", "allday", "all_day", "is all day"},
		"description": {"notes", "body", "details", "comment"},
		"location":    {"where", "place"},
		"private":     {"visibility", "class"},
		"free":        {"show time as", "transparency", "busy"},
		"tentative":   {"status"},
		"reminders":   {"reminder", "alarm", "reminder on/off"},
		"recurrence":  {"rrule", "repeat", "recurrence rule"},
		"uid":         {"id", "event id", "identifier"},
	}

	dateLayouts = []string{"2006-01-02", "01/02/2006", "1/2/2006", "02.01.2006", "2.1.2006", "02/01/2006", "2006/01/02", "20060102"}
	timeLayouts = []string{"15:04", "15:04:05", "3:04 PM", "3:04PM", "3:04:05 PM", "15.04"}
)

type Options struct {
	Timezone string
	MaxItems int
	DayFirst bool
}

func canonical(name string) string {
	name = strings.ToLower(strings.Trim(strings.TrimSpace(strings.TrimPrefix(name, bom)), `"`))
	name = strings.ReplaceAll(name, "_", " ")
	if slices.Contains(header, name) {
		return name
	}
	for col, alts := range aliases {
		if slices.Contains(alts, name) {
			return col
		}
	}
	return ""
}

func Decode(data []byte, o Options) (transfer.Set, error) {
	var set transfer.Set
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	if c, ok := delimiter(data); ok {
		r.Comma = c
	}
	head, err := r.Read()
	if err != nil {
		return set, fmt.Errorf("%w: the file has no header row", domain.ErrInvalid)
	}
	cols := map[string]int{}
	for i, name := range head {
		if c := canonical(name); c != "" {
			if _, dup := cols[c]; !dup {
				cols[c] = i
			}
		}
	}
	if _, ok := cols["subject"]; !ok {
		return set, fmt.Errorf("%w: no column holds the title, expected one named Subject, Summary or Title", domain.ErrInvalid)
	}
	if _, ok := cols["start date"]; !ok {
		return set, fmt.Errorf("%w: no column holds the start date", domain.ErrInvalid)
	}
	cal := transfer.Calendar{}
	for line := 2; ; line++ {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return set, fmt.Errorf("%w: line %d could not be read: %w", domain.ErrInvalid, line, err)
		}
		if o.MaxItems > 0 && len(cal.Series) >= o.MaxItems {
			return set, fmt.Errorf("%w: the file has more than %d rows", domain.ErrInvalid, o.MaxItems)
		}
		field := func(name string) string {
			i, ok := cols[name]
			if !ok || i >= len(rec) {
				return ""
			}
			return strings.TrimSpace(rec[i])
		}
		if strings.Join(rec, "") == "" {
			continue
		}
		where := fmt.Sprintf("line %d", line)
		e, err := row(field, o)
		if err != nil {
			set.Problem(where, "%s", err)
			continue
		}
		cal.Series = append(cal.Series, transfer.Series{Master: e, Where: where})
	}
	if len(cal.Series) == 0 && len(set.Problems) == 0 {
		return set, fmt.Errorf("%w: the file has no rows", domain.ErrInvalid)
	}
	set.Calendars = append(set.Calendars, cal)
	return set, nil
}

func delimiter(data []byte) (rune, bool) {
	line, _, _ := strings.Cut(string(data[:min(len(data), 4096)]), "\n")
	best, count := ',', strings.Count(line, ",")
	for _, c := range []rune{';', '\t'} {
		if n := strings.Count(line, string(c)); n > count {
			best, count = c, n
		}
	}
	return best, count > 0
}

func row(field func(string) string, o Options) (domain.Event, error) {
	title := field("subject")
	if title == "" {
		return domain.Event{}, fmt.Errorf("has no title")
	}
	start, err := parseDate(field("start date"), o.DayFirst)
	if err != nil {
		return domain.Event{}, fmt.Errorf("has an invalid start date %q", field("start date"))
	}
	e := domain.Event{
		UID:          field("uid"),
		Title:        transfer.Clip(title, 1000),
		Body:         transfer.Clip(field("description"), 100000),
		Location:     transfer.Clip(field("location"), 1000),
		Status:       domain.StatusConfirmed,
		Transparency: domain.TransparencyOpaque,
		Visibility:   domain.VisibilityDefault,
		TZ:           o.Timezone,
	}
	end := start
	if v := field("end date"); v != "" {
		if end, err = parseDate(v, o.DayFirst); err != nil {
			return domain.Event{}, fmt.Errorf("has an invalid end date %q", v)
		}
	}
	st, sok := parseClock(field("start time"))
	et, eok := parseClock(field("end time"))
	allDay := truthy(field("all day")) || (!sok && !eok)
	if allDay {
		e.AllDay, e.Start, e.End = true, start, end.AddDate(0, 0, 1)
		if !e.End.After(e.Start) {
			e.End = e.Start.AddDate(0, 0, 1)
		}
	} else {
		loc, lerr := time.LoadLocation(o.Timezone)
		if lerr != nil {
			loc = time.UTC
		}
		if !sok {
			st = 9 * 60
		}
		if !eok {
			et = st + 60
		}
		e.Start = time.Date(start.Year(), start.Month(), start.Day(), 0, st, 0, 0, loc).UTC()
		e.End = time.Date(end.Year(), end.Month(), end.Day(), 0, et, 0, 0, loc).UTC()
		if e.End.Before(e.Start) {
			e.End = e.Start.Add(time.Hour)
		}
	}
	if v := field("private"); truthy(v) || strings.EqualFold(v, "private") || strings.EqualFold(v, "confidential") {
		e.Visibility = domain.VisibilityPrivate
	}
	if v := field("free"); strings.EqualFold(v, "free") || strings.EqualFold(v, "transparent") || truthy(v) {
		e.Transparency = domain.TransparencyTransparent
	}
	if v := field("tentative"); strings.EqualFold(v, "tentative") || truthy(v) {
		e.Status = domain.StatusTentative
	}
	e.RRule = strings.TrimPrefix(strings.ToUpper(field("recurrence")), "RRULE:")
	for _, part := range strings.FieldsFunc(field("reminders"), func(r rune) bool { return r == ',' || r == ';' }) {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err == nil && n >= 0 && n <= domain.MaxReminderMinutes && len(e.Reminders) < domain.MaxReminders {
			e.Reminders = append(e.Reminders, n)
		}
	}
	slices.Sort(e.Reminders)
	e.Reminders = slices.Compact(e.Reminders)
	return e, nil
}

func parseDate(v string, dayFirst bool) (time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, fmt.Errorf("empty")
	}
	if date, _, ok := strings.Cut(v, "T"); ok {
		v = date
	}
	layouts := dateLayouts
	if dayFirst {
		layouts = append([]string{"02/01/2006", "2/1/2006"}, dateLayouts...)
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, v); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid")
}

func parseClock(v string) (int, bool) {
	v = strings.ToUpper(strings.TrimSpace(v))
	if v == "" {
		return 0, false
	}
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, v); err == nil {
			return t.Hour()*60 + t.Minute(), true
		}
	}
	return 0, false
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "y", "on":
		return true
	}
	return false
}

func Encode(set transfer.Set) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(bom)
	w := csv.NewWriter(&buf)
	if err := w.Write([]string{"Subject", "Start Date", "Start Time", "End Date", "End Time", "All Day", "Description",
		"Location", "Private", "Free", "Tentative", "Reminders", "Recurrence", "UID"}); err != nil {
		return nil, err
	}
	yes := func(b bool) string {
		if b {
			return "true"
		}
		return "false"
	}
	for _, c := range set.Calendars {
		for _, s := range c.Series {
			e := s.Master
			z := e.Zone()
			start, end := e.Start.In(z), e.End.In(z)
			startTime, endTime := start.Format("15:04"), end.Format("15:04")
			endDate := end
			if e.AllDay {
				startTime, endTime = "", ""
				endDate = end.AddDate(0, 0, -1)
			}
			mins := make([]string, len(e.Reminders))
			for i, m := range e.Reminders {
				mins[i] = strconv.Itoa(m)
			}
			row := []string{
				e.Title, start.Format(domain.DateLayout), startTime, endDate.Format(domain.DateLayout), endTime,
				yes(e.AllDay), e.Body, e.Location, yes(e.Visibility == domain.VisibilityPrivate),
				yes(e.Transparency == domain.TransparencyTransparent), yes(e.Status == domain.StatusTentative),
				strings.Join(mins, ","), e.RRule, e.UID,
			}
			for i, v := range row {
				if !utf8.ValidString(v) {
					row[i] = strings.ToValidUTF8(v, "")
				}
			}
			if err := w.Write(row); err != nil {
				return nil, err
			}
		}
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}
