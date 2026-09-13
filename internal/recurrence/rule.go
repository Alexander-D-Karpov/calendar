package recurrence

import (
	"strconv"
	"strings"
	"time"

	"github.com/teambition/rrule-go"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

type Rule struct {
	Freq     string
	Interval int
	Until    *time.Time
	Count    int
}

func ParseRule(spec string, loc *time.Location) (Rule, bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return Rule{}, false
	}
	opt, err := rrule.StrToROptionInLocation(spec, loc)
	if err != nil {
		return Rule{}, false
	}
	r := Rule{Freq: freqName(opt.Freq), Interval: opt.Interval, Count: opt.Count}
	if r.Interval < 1 {
		r.Interval = 1
	}
	if !opt.Until.IsZero() {
		until := opt.Until.In(loc)
		r.Until = &until
	}
	return r, r.Freq != ""
}

// UntilDate is the last day the series can produce, in the series timezone. It
// is a property of the rule, so it never moves with the occurrence being edited.
func (r Rule) UntilDate() string {
	if r.Until == nil {
		return ""
	}
	return r.Until.Format(domain.DateLayout)
}

func (r Rule) String(start time.Time, loc *time.Location) string {
	if r.Freq == "" {
		return ""
	}
	parts := []string{"FREQ=" + r.Freq}
	if r.Interval > 1 {
		parts = append(parts, "INTERVAL="+strconv.Itoa(r.Interval))
	}
	switch {
	case r.Count > 0:
		parts = append(parts, "COUNT="+strconv.Itoa(r.Count))
	case r.Until != nil:
		end := time.Date(r.Until.Year(), r.Until.Month(), r.Until.Day(), 23, 59, 59, 0, loc)
		if end.Before(start) {
			end = start
		}
		parts = append(parts, "UNTIL="+end.UTC().Format("20060102T150405Z"))
	}
	return strings.Join(parts, ";")
}

func freqName(f rrule.Frequency) string {
	switch f {
	case rrule.DAILY:
		return "DAILY"
	case rrule.WEEKLY:
		return "WEEKLY"
	case rrule.MONTHLY:
		return "MONTHLY"
	case rrule.YEARLY:
		return "YEARLY"
	}
	return ""
}

func FreqOf(spec string, loc *time.Location) string {
	r, ok := ParseRule(spec, loc)
	if !ok {
		return ""
	}
	if r.Interval > 1 {
		return ""
	}
	return r.Freq
}
