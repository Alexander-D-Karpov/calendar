package recurrence

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/teambition/rrule-go"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const (
	maxIterations = 200_000
	maxRuleLength = 1000
	allDayMargin  = 14 * time.Hour
)

var ErrTooMany = fmt.Errorf("%w: too many occurrences in the range, narrow it", domain.ErrInvalid)

type Series struct {
	Start    time.Time
	Duration time.Duration
	rule     *rrule.RRule
	extra    []time.Time
	excluded map[int64]bool
}

func FromEvent(e domain.Event) (Series, error) {
	start := dtstart(e)
	s := Series{Start: start, Duration: e.Duration(), excluded: make(map[int64]bool, len(e.ExDate))}
	for _, x := range e.ExDate {
		s.excluded[x.UnixMicro()] = true
	}
	s.extra = make([]time.Time, 0, len(e.RDate)+1)
	s.extra = append(s.extra, start)
	for _, r := range e.RDate {
		s.extra = append(s.extra, r.In(start.Location()))
	}
	slices.SortFunc(s.extra, func(a, b time.Time) int { return a.Compare(b) })
	if e.RRule != "" {
		opt, err := parseOption(e.RRule, start.Location())
		if err != nil {
			return Series{}, err
		}
		opt.Dtstart = start
		r, err := rrule.NewRRule(*opt)
		if err != nil {
			return Series{}, err
		}
		s.rule = r
	}
	return s, nil
}

func (s Series) Between(from, to time.Time, limit int) ([]time.Time, error) {
	var out []time.Time
	full := false
	err := s.each(func(t time.Time) bool {
		if !t.Before(to) {
			return false
		}
		if Overlaps(t, t.Add(s.Duration), from, to) {
			if len(out) >= limit {
				full = true
				return false
			}
			out = append(out, t.UTC())
		}
		return true
	})
	if err == nil && full {
		err = ErrTooMany
	}
	return out, err
}

func (s Series) Contains(t time.Time) (bool, error) {
	found := false
	err := s.each(func(x time.Time) bool {
		if x.Equal(t) {
			found = true
		}
		return x.Before(t)
	})
	return found, err
}

func (s Series) LastStart() (time.Time, bool) {
	last := s.extra[len(s.extra)-1]
	if s.rule == nil {
		return last, true
	}
	opt := s.rule.OrigOptions
	if !opt.Until.IsZero() {
		if opt.Until.After(last) {
			last = opt.Until
		}
		return last, true
	}
	if opt.Count == 0 {
		return time.Time{}, false
	}
	next := s.rule.Iterator()
	for range maxIterations {
		t, ok := next()
		if !ok {
			return last, true
		}
		if t.After(last) {
			last = t
		}
	}
	return time.Time{}, false
}

func (s Series) each(fn func(time.Time) bool) error {
	var next func() (time.Time, bool)
	var cur time.Time
	ok := false
	steps := 0
	advance := func() {
		cur, ok = next()
		steps++
	}
	if s.rule != nil {
		next = s.rule.Iterator()
		advance()
	}
	i := 0
	var last time.Time
	started := false
	for {
		if steps > maxIterations {
			return ErrTooMany
		}
		var t time.Time
		switch {
		case ok && (i >= len(s.extra) || !s.extra[i].Before(cur)):
			t = cur
			advance()
		case i < len(s.extra):
			t = s.extra[i]
			i++
		default:
			return nil
		}
		if started && !t.After(last) {
			continue
		}
		last, started = t, true
		if s.excluded[t.UnixMicro()] {
			continue
		}
		if !fn(t) {
			return nil
		}
	}
}

func Overlaps(start, end, from, to time.Time) bool {
	if !start.Before(to) {
		return false
	}
	if end.After(from) {
		return true
	}
	return !end.After(start) && !start.Before(from)
}

func Span(e domain.Event) (time.Time, *time.Time) {
	from, to := e.Start, e.End
	if e.IsMaster() {
		if s, err := FromEvent(e); err == nil {
			from = s.extra[0]
			last, bounded := s.LastStart()
			if !bounded {
				if e.AllDay {
					from = from.Add(-allDayMargin)
				}
				return from.UTC(), nil
			}
			to = last.Add(e.Duration())
		}
	}
	if e.AllDay {
		from, to = from.Add(-allDayMargin), to.Add(allDayMargin)
	}
	from, to = from.UTC(), to.UTC()
	return from, &to
}

func Normalize(rule string, loc *time.Location) (string, error) {
	opt, err := parseOption(rule, loc)
	if err != nil {
		return "", err
	}
	if _, err := rrule.NewRRule(*opt); err != nil {
		return "", errors.New("is not a valid RRULE")
	}
	return opt.RRuleString(), nil
}

func Split(e domain.Event, at time.Time) (string, string, error) {
	start := dtstart(e)
	opt, err := parseOption(e.RRule, start.Location())
	if err != nil {
		return "", "", err
	}
	head, tail := *opt, *opt
	if opt.Count == 0 {
		head.Until = at.Add(-time.Second).UTC()
		return head.RRuleString(), tail.RRuleString(), nil
	}
	opt.Dtstart = start
	r, err := rrule.NewRRule(*opt)
	if err != nil {
		return "", "", err
	}
	n := 0
	next := r.Iterator()
	for n < opt.Count {
		t, ok := next()
		if !ok || !t.Before(at) {
			break
		}
		n++
	}
	var headRule, tailRule string
	if n > 0 {
		head.Count = n
		headRule = head.RRuleString()
	}
	if opt.Count-n > 0 {
		tail.Count = opt.Count - n
		tailRule = tail.RRuleString()
	}
	return headRule, tailRule, nil
}

func dtstart(e domain.Event) time.Time {
	if e.AllDay {
		return e.Start.UTC()
	}
	return e.Start.In(e.Zone())
}

func parseOption(rule string, loc *time.Location) (*rrule.ROption, error) {
	rule = strings.TrimSpace(rule)
	if len(rule) > maxRuleLength {
		return nil, errors.New("must be at most 1000 characters")
	}
	rule = strings.TrimPrefix(strings.ToUpper(rule), "RRULE:")
	opt, err := rrule.StrToROptionInLocation(rule, loc)
	if err != nil {
		return nil, errors.New("is not a valid RRULE")
	}
	switch {
	case opt.Freq > rrule.DAILY:
		return nil, errors.New("must repeat daily or less often")
	case len(opt.Byhour) > 0 || len(opt.Byminute) > 0 || len(opt.Bysecond) > 0:
		return nil, errors.New("BYHOUR, BYMINUTE and BYSECOND are not supported")
	case opt.Count > 0 && !opt.Until.IsZero():
		return nil, errors.New("cannot combine COUNT and UNTIL")
	case opt.Count < 0 || opt.Interval < 0:
		return nil, errors.New("is not a valid RRULE")
	}
	return opt, nil
}

func Next(rule string, start time.Time) (time.Time, string, bool, error) {
	opt, err := parseOption(rule, start.Location())
	if err != nil {
		return time.Time{}, "", false, err
	}
	if opt.Count == 1 {
		return time.Time{}, "", false, nil
	}
	rest := *opt
	if opt.Count > 1 {
		rest.Count = opt.Count - 1
	}
	opt.Dtstart = start
	r, err := rrule.NewRRule(*opt)
	if err != nil {
		return time.Time{}, "", false, err
	}
	next := r.After(start, false)
	if next.IsZero() {
		return time.Time{}, "", false, nil
	}
	return next, rest.RRuleString(), true, nil
}
