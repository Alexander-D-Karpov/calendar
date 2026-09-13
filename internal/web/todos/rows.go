package todos

import (
	"maps"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/store"
	"github.com/Alexander-D-Karpov/calendar/internal/view"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

const (
	upcomingDays   = 14
	completedLimit = 200
)

var (
	filterOptions   = []web.Option{{Value: "today", Label: "Today"}, {Value: "upcoming", Label: "Upcoming"}, {Value: "overdue", Label: "Overdue"}, {Value: "completed", Label: "Completed"}}
	priorityOptions = []web.Option{{Value: "0", Label: "None"}, {Value: "1", Label: "Low"}, {Value: "2", Label: "Medium"}, {Value: "3", Label: "High"}}
	showOptions     = []web.Option{{Value: "auto", Label: "When a time is set"}, {Value: "yes", Label: "Always"}, {Value: "no", Label: "Never"}}
)

type link struct {
	Label   string
	Href    string
	Hex     string
	Current bool
}

type todoRow struct {
	Todo     domain.Todo
	Due      string
	Overdue  bool
	Priority string
	Progress string
	ListName string
	Hex      string
	CSRF     string
	Back     string
	Subtasks []todoRow
}

type indexView struct {
	Heading   string
	Filters   []link
	Lists     []link
	Rows      []todoRow
	Colors    []string
	Back      string
	NewHref   string
	QuickList string
}

type detailView struct {
	Todo      domain.Todo
	List      domain.TodoList
	Parent    *domain.Todo
	Due       string
	Overdue   bool
	Priority  string
	Progress  string
	Reminders string
	Subtasks  []todoRow
	Back      string
	Self      string
	Hex       string
	Colors    []string
}

type formView struct {
	Action      string
	Cancel      string
	Back        string
	ETag        string
	Lists       []domain.TodoList
	AllowList   bool
	ParentTitle string
	Repeats     []web.RepeatOption
	Priorities  []web.Option
	Shows       []web.Option
}

type rowContext struct {
	lists    map[domain.ID]domain.TodoList
	today    time.Time
	clock24  bool
	showList bool
	csrf     string
	back     string
}

func (c rowContext) row(t domain.Todo) todoRow {
	l := c.lists[t.ListID]
	r := todoRow{
		Todo:     t,
		Due:      dueLabel(t, c.today, c.clock24),
		Overdue:  overdue(t, c.today),
		Priority: priorityLabel(t.Priority),
		Progress: progress(t),
		Hex:      view.Hex(l.Color),
		CSRF:     c.csrf,
		Back:     c.back,
	}
	if c.showList {
		r.ListName = l.Name
	}
	return r
}

func buildRows(todos []domain.Todo, c rowContext) []todoRow {
	top := map[domain.ID]int{}
	var rows []todoRow
	for _, t := range todos {
		if t.ParentID == nil {
			top[t.ID] = len(rows)
			rows = append(rows, c.row(t))
		}
	}
	for _, t := range todos {
		if t.ParentID == nil {
			continue
		}
		if i, ok := top[*t.ParentID]; ok {
			rows[i].Subtasks = append(rows[i].Subtasks, c.row(t))
			continue
		}
		rows = append(rows, c.row(t))
	}
	return rows
}

func selection(today time.Time, cur *domain.TodoList, name string) (store.TodoFilter, indexView) {
	tomorrow := today.AddDate(0, 0, 1)
	iv := indexView{Heading: "Todos", NewHref: "/todos/new"}
	if cur != nil {
		iv.Heading, iv.QuickList = cur.Name, cur.ID.String()
		iv.NewHref += "?list=" + cur.ID.String()
		return store.TodoFilter{Lists: []domain.ID{cur.ID}, Status: domain.TodoOpen, Order: store.OrderByPosition}, iv
	}
	f := store.TodoFilter{Status: domain.TodoOpen, Scheduled: true, Order: store.OrderByDue}
	switch name {
	case "today":
		f.DueTo = &tomorrow
	case "upcoming":
		end := today.AddDate(0, 0, upcomingDays+1)
		f.DueFrom, f.DueTo = &tomorrow, &end
	case "overdue":
		f.DueTo = &today
	case "completed":
		f = store.TodoFilter{Status: domain.TodoCompleted, Order: store.OrderByCompleted, Limit: completedLimit}
	}
	if l := web.LabelOf(filterOptions, name); l != "" {
		iv.Heading = l
	}
	return f, iv
}

func dueLabel(t domain.Todo, today time.Time, clock24 bool) string {
	if t.DueDate == nil {
		return ""
	}
	d := *t.DueDate
	var s string
	switch days := int(math.Round(d.Sub(today).Hours() / 24)); {
	case days == 0:
		s = "Today"
	case days == 1:
		s = "Tomorrow"
	case days == -1:
		s = "Yesterday"
	case days > 1 && days < 7:
		s = d.Format("Monday")
	case d.Year() == today.Year():
		s = d.Format("Mon 2 Jan")
	default:
		s = d.Format("2 Jan 2006")
	}
	if t.DueTime != nil {
		s += " " + view.Clock(time.Date(2000, 1, 1, 0, *t.DueTime, 0, 0, time.UTC), clock24)
		if t.Duration > 0 {
			s += ", " + durationLabel(t.Duration)
		}
	}
	return s
}

func durationLabel(m int) string {
	switch {
	case m < 60:
		return strconv.Itoa(m) + " min"
	case m%60 == 0:
		return strconv.Itoa(m/60) + " h"
	}
	return strconv.Itoa(m/60) + " h " + strconv.Itoa(m%60) + " min"
}

func overdue(t domain.Todo, today time.Time) bool {
	return !t.Done() && t.DueDate != nil && t.DueDate.Before(today)
}

func priorityLabel(p int) string {
	if p <= 0 || p >= len(priorityOptions) {
		return ""
	}
	return priorityOptions[p].Label
}

func progress(t domain.Todo) string {
	if len(t.Checks) == 0 {
		return ""
	}
	return strconv.Itoa(t.ChecksDone()) + "/" + strconv.Itoa(len(t.Checks))
}

func listColors(lists []domain.TodoList) []string {
	set := map[string]bool{}
	for _, l := range lists {
		set[view.Hex(l.Color)] = true
	}
	return slices.Sorted(maps.Keys(set))
}

func listMap(lists []domain.TodoList) map[domain.ID]domain.TodoList {
	m := make(map[domain.ID]domain.TodoList, len(lists))
	for _, l := range lists {
		m[l.ID] = l
	}
	return m
}

func findList(lists []domain.TodoList, raw string) *domain.TodoList {
	id, err := domain.ParseID(raw)
	if err != nil {
		return nil
	}
	for i := range lists {
		if lists[i].ID == id {
			return &lists[i]
		}
	}
	return nil
}

func defaultList(lists []domain.TodoList) *domain.TodoList {
	for i := range lists {
		if lists[i].IsDefault {
			return &lists[i]
		}
	}
	if len(lists) > 0 {
		return &lists[0]
	}
	return nil
}

func todoPatch(f *web.Form, allowList bool) (domain.TodoPatch, error) {
	var v domain.ValidationError
	has := f.Values.Has
	p := domain.TodoPatch{Title: domain.Some(f.Get("title"))}
	if has("body") {
		p.Body = domain.Some(strings.TrimRight(f.Values.Get("body"), " \t\r\n"))
	}
	if s := f.Get("list_id"); allowList && s != "" {
		p.ListID = domain.Some(s)
	}
	if s := f.Get("parent_id"); s != "" {
		p.ParentID = domain.Some(s)
	}
	if has("due_date") {
		p.DueDate = domain.Some(f.Get("due_date"))
	}
	if has("due_time") {
		p.DueTime = domain.Some(f.Get("due_time"))
	}
	if has("duration") {
		s := f.Get("duration")
		switch n, err := strconv.Atoi(s); {
		case s == "" || (has("due_time") && f.Get("due_time") == ""):
			p.DurationMin = domain.Opt[int]{Set: true, Null: true}
		case err == nil:
			p.DurationMin = domain.Some(n)
		default:
			v.Add("duration_min", "must be a number of minutes")
		}
	}
	if has("timezone") {
		p.Timezone = domain.Some(f.Get("timezone"))
	}
	if has("repeat") {
		p.RRule = domain.Some(web.RepeatRule(f))
	}
	if has("priority") {
		if n, err := strconv.Atoi(f.Get("priority")); err == nil {
			p.Priority = domain.Some(n)
		} else {
			v.Add("priority", "must be none, low, medium or high")
		}
	}
	if has("reminders") {
		if mins, ok := web.ParseInts(f.Get("reminders")); ok {
			p.Reminders = domain.Some(mins)
		} else {
			v.Add("reminders", "must be minutes separated by commas, like 10, 60")
		}
	}
	switch f.Get("show") {
	case "yes":
		p.ShowOnCalendar = domain.Some(true)
	case "no":
		p.ShowOnCalendar = domain.Some(false)
	case "auto":
		p.ShowOnCalendar = domain.Some(f.Get("due_date") != "" && f.Get("due_time") != "")
	}
	return p, v.Err()
}

func formValues(t domain.Todo, tz string) url.Values {
	v := url.Values{
		"title":     {t.Title},
		"body":      {t.Body},
		"list_id":   {t.ListID.String()},
		"priority":  {strconv.Itoa(t.Priority)},
		"reminders": {web.JoinInts(t.Reminders)},
		"timezone":  {tz},
		"due_date":  {""},
		"due_time":  {""},
		"duration":  {""},
	}
	if t.TZ != "" {
		v.Set("timezone", t.TZ)
	}
	if t.DueDate != nil {
		v.Set("due_date", t.DueDate.Format(domain.DateLayout))
	}
	if t.DueTime != nil {
		v.Set("due_time", domain.FormatClock(*t.DueTime))
	}
	if t.Duration > 0 {
		v.Set("duration", strconv.Itoa(t.Duration))
	}
	repeat, custom := web.RepeatValue(t.RRule)
	v.Set("repeat", repeat)
	v.Set("rrule", custom)
	switch {
	case t.ShowOnCalendar == (t.DueTime != nil):
		v.Set("show", "auto")
	case t.ShowOnCalendar:
		v.Set("show", "yes")
	default:
		v.Set("show", "no")
	}
	return v
}
