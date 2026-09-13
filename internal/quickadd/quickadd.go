package quickadd

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

type Result struct {
	Title    string
	Date     *time.Time
	Time     *domain.TimeOfDay
	End      *domain.TimeOfDay
	Duration int
	AllDay   bool
	Priority int
	List     string
	Matched  bool
}

type Options struct {
	Now       time.Time
	Loc       *time.Location
	WeekStart time.Weekday
}

var (
	timeRe     = regexp.MustCompile(`(?i)\b(?:at\s+)?([01]?\d|2[0-3])[:.]([0-5]\d)\s*(am|pm)?\b`)
	hourRe     = regexp.MustCompile(`(?i)\bat\s+(1[0-2]|[1-9])\s*(am|pm)\b`)
	rangeRe    = regexp.MustCompile(`(?i)\b([01]?\d|2[0-3])[:.]([0-5]\d)\s*(?:-|–|to)\s*([01]?\d|2[0-3])[:.]([0-5]\d)\b`)
	durationRe = regexp.MustCompile(`(?i)\bfor\s+(\d{1,3})\s*(m|min|mins|minutes|h|hr|hrs|hours)\b`)
	inRe       = regexp.MustCompile(`(?i)\bin\s+(\d{1,3})\s*(d|days?|w|weeks?)\b`)
	isoRe      = regexp.MustCompile(`\b(\d{4})-(\d{2})-(\d{2})\b`)
	dayMonthRe = regexp.MustCompile(`(?i)\b(\d{1,2})\s+(jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*\b`)
	monthDayRe = regexp.MustCompile(`(?i)\b(jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*\s+(\d{1,2})\b`)
	listRe     = regexp.MustCompile(`(?:^|\s)#([\p{L}\p{N}_-]{1,40})`)
	priorityRe = regexp.MustCompile(`(?:^|\s)(!{1,3})(?:\s|$)`)
	spaceRe    = regexp.MustCompile(`\s{2,}`)
)

var months = map[string]time.Month{
	"jan": time.January, "feb": time.February, "mar": time.March, "apr": time.April,
	"may": time.May, "jun": time.June, "jul": time.July, "aug": time.August,
	"sep": time.September, "oct": time.October, "nov": time.November, "dec": time.December,
}

var weekdays = map[string]time.Weekday{
	"sunday": time.Sunday, "sun": time.Sunday,
	"monday": time.Monday, "mon": time.Monday,
	"tuesday": time.Tuesday, "tue": time.Tuesday, "tues": time.Tuesday,
	"wednesday": time.Wednesday, "wed": time.Wednesday,
	"thursday": time.Thursday, "thu": time.Thursday, "thur": time.Thursday, "thurs": time.Thursday,
	"friday": time.Friday, "fri": time.Friday,
	"saturday": time.Saturday, "sat": time.Saturday,
}

type cut struct {
	start, end int
}

type parser struct {
	text string
	cuts []cut
	out  Result
	opt  Options
}

func Parse(input string, o Options) Result {
	if o.Loc == nil {
		o.Loc = time.UTC
	}
	p := &parser{text: input, opt: o}
	p.priority()
	p.list()
	p.timeRange()
	p.duration()
	p.clock()
	p.date()
	p.out.Title = p.remainder()
	p.out.Matched = len(p.cuts) > 0
	if p.out.Title == "" {
		return Result{Title: strings.TrimSpace(input)}
	}
	return p.out
}

func (p *parser) take(re *regexp.Regexp, fn func(m []string) bool) bool {
	for _, loc := range re.FindAllStringSubmatchIndex(p.text, -1) {
		// Several patterns anchor on a surrounding space, and RE2 has no
		// lookahead to match one without consuming it. Cutting the space too
		// would hide the next token: "!! #work" would lose the list, because
		// its match starts on the space the priority already claimed.
		start, end := trimSpace(p.text, loc[0], loc[1])
		if p.taken(start, end) {
			continue
		}
		m := make([]string, 0, len(loc)/2)
		for i := 0; i < len(loc); i += 2 {
			if loc[i] < 0 {
				m = append(m, "")
				continue
			}
			m = append(m, p.text[loc[i]:loc[i+1]])
		}
		if !fn(m) {
			continue
		}
		p.cuts = append(p.cuts, cut{start, end})
		return true
	}
	return false
}

func trimSpace(s string, start, end int) (int, int) {
	for start < end && isSpace(s[start]) {
		start++
	}
	for end > start && isSpace(s[end-1]) {
		end--
	}
	return start, end
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

func (p *parser) taken(start, end int) bool {
	for _, c := range p.cuts {
		if start < c.end && c.start < end {
			return true
		}
	}
	return false
}

func (p *parser) remainder() string {
	out := []byte(p.text)
	for _, c := range p.cuts {
		for i := c.start; i < c.end; i++ {
			out[i] = ' '
		}
	}
	return strings.TrimSpace(spaceRe.ReplaceAllString(string(out), " "))
}

func (p *parser) priority() {
	p.take(priorityRe, func(m []string) bool {
		p.out.Priority = len(m[1])
		return true
	})
}

func (p *parser) list() {
	p.take(listRe, func(m []string) bool {
		p.out.List = m[1]
		return true
	})
}

func (p *parser) timeRange() {
	p.take(rangeRe, func(m []string) bool {
		from, ok := clockOf(m[1], m[2], "")
		to, ok2 := clockOf(m[3], m[4], "")
		if !ok || !ok2 {
			return false
		}
		p.out.Time, p.out.End = &from, &to
		return true
	})
}

func (p *parser) duration() {
	p.take(durationRe, func(m []string) bool {
		n, err := strconv.Atoi(m[1])
		if err != nil || n <= 0 {
			return false
		}
		if strings.HasPrefix(strings.ToLower(m[2]), "h") {
			n *= 60
		}
		if n > 24*60 {
			return false
		}
		p.out.Duration = n
		return true
	})
}

func (p *parser) clock() {
	if p.out.Time != nil {
		return
	}
	if p.take(timeRe, func(m []string) bool {
		t, ok := clockOf(m[1], m[2], m[3])
		if !ok {
			return false
		}
		p.out.Time = &t
		return true
	}) {
		return
	}
	p.take(hourRe, func(m []string) bool {
		t, ok := clockOf(m[1], "00", m[2])
		if !ok {
			return false
		}
		p.out.Time = &t
		return true
	})
}

func clockOf(hh, mm, meridiem string) (domain.TimeOfDay, bool) {
	h, err := strconv.Atoi(hh)
	if err != nil {
		return domain.TimeOfDay{}, false
	}
	m, err := strconv.Atoi(mm)
	if err != nil {
		return domain.TimeOfDay{}, false
	}
	switch strings.ToLower(meridiem) {
	case "am":
		if h == 12 {
			h = 0
		}
	case "pm":
		if h < 12 {
			h += 12
		}
	}
	if h > 23 || m > 59 {
		return domain.TimeOfDay{}, false
	}
	return domain.TimeOfDay{Hour: h, Minute: m}, true
}

func (p *parser) date() {
	today := domain.DayOf(p.opt.Now.In(p.opt.Loc))
	if p.word("today", today) || p.word("tonight", today) {
		return
	}
	if p.word("tomorrow", today.AddDate(0, 0, 1)) || p.word("tmr", today.AddDate(0, 0, 1)) {
		return
	}
	if p.word("yesterday", today.AddDate(0, 0, -1)) {
		return
	}
	if p.take(isoRe, func(m []string) bool {
		d, err := time.ParseInLocation(domain.DateLayout, m[0], p.opt.Loc)
		if err != nil {
			return false
		}
		p.set(d)
		return true
	}) {
		return
	}
	if p.take(inRe, func(m []string) bool {
		n, err := strconv.Atoi(m[1])
		if err != nil || n <= 0 {
			return false
		}
		if strings.HasPrefix(strings.ToLower(m[2]), "w") {
			n *= 7
		}
		p.set(today.AddDate(0, 0, n))
		return true
	}) {
		return
	}
	if p.monthDay(dayMonthRe, 1, 2) || p.monthDay(monthDayRe, 2, 1) {
		return
	}
	p.weekday(today)
}

func (p *parser) word(word string, day time.Time) bool {
	re := regexp.MustCompile(`(?i)\b` + word + `\b`)
	return p.take(re, func([]string) bool {
		p.set(day)
		return true
	})
}

func (p *parser) monthDay(re *regexp.Regexp, dayIdx, monIdx int) bool {
	return p.take(re, func(m []string) bool {
		day, err := strconv.Atoi(m[dayIdx])
		mon, ok := months[strings.ToLower(m[monIdx])[:3]]
		if err != nil || !ok || day < 1 || day > 31 {
			return false
		}
		now := p.opt.Now.In(p.opt.Loc)
		d := time.Date(now.Year(), mon, day, 0, 0, 0, 0, p.opt.Loc)
		if d.Month() != mon {
			return false
		}
		if d.Before(domain.DayOf(now)) {
			d = d.AddDate(1, 0, 0)
		}
		p.set(d)
		return true
	})
}

// weekday resolves a bare weekday name to the next such day, and "next monday"
// to the one after that, matching how people say it.
func (p *parser) weekday(today time.Time) {
	re := regexp.MustCompile(`(?i)\b(next\s+|this\s+)?(sunday|sun|monday|mon|tuesday|tues|tue|wednesday|wed|thursday|thurs|thur|thu|friday|fri|saturday|sat)\b`)
	p.take(re, func(m []string) bool {
		want, ok := weekdays[strings.ToLower(m[2])]
		if !ok {
			return false
		}
		delta := (int(want) - int(today.Weekday()) + 7) % 7
		if delta == 0 {
			delta = 7
		}
		d := today.AddDate(0, 0, delta)
		if strings.EqualFold(strings.TrimSpace(m[1]), "next") {
			d = d.AddDate(0, 0, 7)
		}
		p.set(d)
		return true
	})
}

func (p *parser) set(d time.Time) {
	day := domain.DayOf(d)
	p.out.Date = &day
	p.out.AllDay = p.out.Time == nil
}
