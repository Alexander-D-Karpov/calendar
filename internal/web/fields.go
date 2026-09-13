package web

import (
	"strconv"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/recurrence"
)

type RepeatOption struct {
	Value string
	Label string
	Rule  string
}

var RepeatOptions = []RepeatOption{
	{"none", "Does not repeat", ""},
	{"daily", "Every day", "FREQ=DAILY"},
	{"weekdays", "Every weekday", "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"},
	{"weekly", "Every week", "FREQ=WEEKLY"},
	{"monthly", "Every month", "FREQ=MONTHLY"},
	{"yearly", "Every year", "FREQ=YEARLY"},
	{"custom", "Custom rule", ""},
}

func RepeatRule(f *Form) string {
	value := f.Get("repeat")
	for _, o := range RepeatOptions {
		if o.Value != value {
			continue
		}
		if value == "custom" {
			return f.Get("rrule")
		}
		return o.Rule
	}
	return ""
}

func RepeatValue(rule string) (string, string) {
	if rule == "" {
		return "none", ""
	}
	norm := func(r string) string {
		if n, err := recurrence.Normalize(r, time.UTC); err == nil {
			return n
		}
		return r
	}
	want := norm(rule)
	for _, o := range RepeatOptions {
		if o.Rule != "" && norm(o.Rule) == want {
			return o.Value, ""
		}
	}
	return "custom", rule
}

func ReminderLabel(mins []int) string {
	parts := make([]string, len(mins))
	for i, m := range mins {
		switch {
		case m == 0:
			parts[i] = "at start"
		case m%1440 == 0:
			parts[i] = Plural(m/1440, "day") + " before"
		case m%60 == 0:
			parts[i] = Plural(m/60, "hour") + " before"
		default:
			parts[i] = Plural(m, "minute") + " before"
		}
	}
	return strings.Join(parts, ", ")
}

func ParseInts(s string) ([]int, bool) {
	out := []int{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

func JoinInts(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ", ")
}

type Option struct {
	Value string
	Label string
}

func LabelOf(opts []Option, value string) string {
	for _, o := range opts {
		if o.Value == value {
			return o.Label
		}
	}
	return ""
}
