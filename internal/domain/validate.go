package domain

import (
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxReminders       = 5
	MaxReminderMinutes = 40320
)

var colorPattern = regexp.MustCompile(`^#[0-9a-f]{6}$`)

func NormalizeColor(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	return s, colorPattern.MatchString(s)
}

func ValidTimezone(tz string) bool {
	if tz == "" || tz == "Local" {
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}

func (e *ValidationError) Length(field, s string, lo, hi int) {
	n := utf8.RuneCountInString(s)
	switch {
	case n >= lo && n <= hi:
	case lo == 0:
		e.Addf(field, "must be at most %d characters", hi)
	default:
		e.Addf(field, "must be %d to %d characters", lo, hi)
	}
}

func NormalizeReminders(v *ValidationError, field string, in []int) []int {
	out := slices.Clone(in)
	slices.Sort(out)
	out = slices.Compact(out)
	if len(out) > MaxReminders {
		v.Addf(field, "must have at most %d reminders", MaxReminders)
	}
	for _, m := range out {
		if m < 0 || m > MaxReminderMinutes {
			v.Addf(field, "must be between 0 and %d minutes", MaxReminderMinutes)
			break
		}
	}
	if out == nil {
		out = []int{}
	}
	return out
}
