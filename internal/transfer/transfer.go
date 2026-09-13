package transfer

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

type Series struct {
	Master    domain.Event
	Overrides []domain.Event
	Modified  time.Time
	Where     string
}

type Calendar struct {
	Name        string
	Color       string
	Timezone    string
	Description string
	Hidden      bool
	Reminders   []int
	Series      []Series
}

type TodoNode struct {
	Todo     domain.Todo
	Modified time.Time
	Where    string
	Subtasks []TodoNode
}

type List struct {
	Name  string
	Color string
	Todos []TodoNode
}

type Problem struct {
	Where   string `json:"where"`
	Message string `json:"message"`
}

type Set struct {
	Calendars []Calendar
	Lists     []List
	Settings  *domain.SettingsPatch
	Problems  []Problem
}

func (s *Set) Problem(where, format string, args ...any) {
	s.Problems = append(s.Problems, Problem{Where: where, Message: fmt.Sprintf(format, args...)})
}

func (s Set) Size() int {
	n := 0
	for _, c := range s.Calendars {
		for _, sr := range c.Series {
			n += 1 + len(sr.Overrides)
		}
	}
	for _, l := range s.Lists {
		for _, t := range l.Todos {
			n += 1 + len(t.Subtasks)
		}
	}
	return n
}

func Clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

var windowsZones = map[string]string{
	"UTC":                            "UTC",
	"GMT Standard Time":              "Europe/London",
	"W. Europe Standard Time":        "Europe/Berlin",
	"Central Europe Standard Time":   "Europe/Budapest",
	"Central European Standard Time": "Europe/Warsaw",
	"Romance Standard Time":          "Europe/Paris",
	"FLE Standard Time":              "Europe/Helsinki",
	"E. Europe Standard Time":        "Europe/Chisinau",
	"Russian Standard Time":          "Europe/Moscow",
	"Eastern Standard Time":          "America/New_York",
	"Central Standard Time":          "America/Chicago",
	"Mountain Standard Time":         "America/Denver",
	"Pacific Standard Time":          "America/Los_Angeles",
	"China Standard Time":            "Asia/Shanghai",
	"Tokyo Standard Time":            "Asia/Tokyo",
	"India Standard Time":            "Asia/Kolkata",
}

func Zone(name string) (string, bool) {
	name = strings.Trim(strings.TrimSpace(name), `"`)
	if iana, ok := windowsZones[name]; ok {
		return iana, true
	}
	name = strings.TrimPrefix(name, "/")
	if domain.ValidTimezone(name) {
		return name, true
	}
	return "", false
}

func ParseICSTime(v, zone string, date bool) (time.Time, bool, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil || zone == "" {
		loc = time.UTC
	}
	switch {
	case date || len(v) == 8:
		if len(v) != 8 {
			return time.Time{}, true, errors.New("invalid date")
		}
		d, err := time.Parse("20060102", v)
		return d, true, err
	case strings.HasSuffix(v, "Z"):
		t, err := time.Parse("20060102T150405Z", v)
		return t.UTC(), false, err
	}
	t, err := time.ParseInLocation("20060102T150405", v, loc)
	return t.UTC(), false, err
}
