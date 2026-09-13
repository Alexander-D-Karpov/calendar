package account

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/view"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

const (
	profilePath  = "/settings/profile"
	defaultSleep = "23:00"
	defaultWake  = "07:00"
)

var (
	weekStarts  = []web.Option{{Value: "1", Label: "Monday"}, {Value: "0", Label: "Sunday"}, {Value: "6", Label: "Saturday"}}
	timeFormats = []web.Option{{Value: domain.TimeFormat24, Label: "24-hour"}, {Value: domain.TimeFormat12, Label: "12-hour"}}
	sleepOrder  = []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}

	dedupPolicies = []web.Option{
		{Value: domain.DedupFlag, Label: "Show them for review"},
		{Value: domain.DedupAutoExact, Label: "Delete exact copies on their own"},
		{Value: domain.DedupAutoPrint, Label: "Delete copies that match title, time and place"},
		{Value: domain.DedupOff, Label: "Do not check"},
	}
)

type sleepDay struct {
	Weekday int
	Label   string
}

type profileView struct {
	WeekStarts  []web.Option
	TimeFormats []web.Option
	Views       []web.Option
	Dedup       []web.Option
	Days        []sleepDay
}

func (a *Pages) profilePage(w http.ResponseWriter, r *http.Request) {
	u, ws, err := a.settings.Get(r.Context(), auth.PrincipalFrom(r.Context()).UserID)
	if err != nil {
		a.site.Fail(w, r, err)
		return
	}
	a.renderProfile(w, r, http.StatusOK, web.NewForm(profileValues(u, ws)))
}

func (a *Pages) saveProfile(w http.ResponseWriter, r *http.Request) {
	f, ok := a.site.ParseForm(w, r)
	if !ok {
		return
	}
	p, err := profilePatch(f)
	if err == nil {
		_, _, err = a.settings.Update(r.Context(), auth.PrincipalFrom(r.Context()).UserID, p, "")
	}
	if err != nil {
		a.site.Retry(w, r, err, f, func(status int) { a.renderProfile(w, r, status, f) })
		return
	}
	a.site.Flash(w, r, "ok", "Settings saved.")
	a.site.Redirect(w, r, profilePath)
}

func (a *Pages) renderProfile(w http.ResponseWriter, r *http.Request, status int, f *web.Form) {
	pv := profileView{WeekStarts: weekStarts, TimeFormats: timeFormats, Dedup: dedupPolicies}
	for _, k := range view.Kinds {
		pv.Views = append(pv.Views, web.Option{Value: string(k), Label: k.Label()})
	}
	for _, d := range sleepOrder {
		pv.Days = append(pv.Days, sleepDay{Weekday: int(d), Label: d.String()})
	}
	a.site.Render(w, r, status, "settings/profile", &web.Page{Title: "Profile", Nav: "settings/profile", Form: f, Data: pv})
}

func sleepKey(field string, day int) string {
	return fmt.Sprintf("sleep_%s_%d", field, day)
}

func profileValues(u domain.User, ws []domain.SleepWindow) url.Values {
	v := url.Values{
		"display_name":       {u.DisplayName},
		"timezone":           {u.Timezone},
		"week_start":         {strconv.Itoa(u.WeekStart)},
		"time_format":        {u.TimeFormat},
		"default_view":       {u.DefaultView},
		"dedup_policy":       {u.DedupPolicy},
		"date_only_reminder": {domain.FormatClock(u.DateOnlyReminder)},
		"sleep_mode":         {"daily"},
		"sleep_start":        {defaultSleep},
		"sleep_end":          {defaultWake},
	}
	if u.SleepEnabled {
		v.Set("sleep", "1")
	}
	for d := range 7 {
		v.Set(sleepKey("start", d), defaultSleep)
		v.Set(sleepKey("end", d), defaultWake)
		if len(ws) == 0 {
			v.Set(sleepKey("on", d), "1")
		}
	}
	for _, sw := range ws {
		d := int(sw.Weekday)
		v.Set(sleepKey("on", d), "1")
		v.Set(sleepKey("start", d), domain.FormatClock(sw.Start))
		v.Set(sleepKey("end", d), domain.FormatClock(sw.End))
	}
	if len(ws) > 0 {
		v.Set("sleep_start", domain.FormatClock(ws[0].Start))
		v.Set("sleep_end", domain.FormatClock(ws[0].End))
		if !uniformSleep(ws) {
			v.Set("sleep_mode", "custom")
		}
	}
	return v
}

func uniformSleep(ws []domain.SleepWindow) bool {
	if len(ws) != 7 {
		return false
	}
	for _, sw := range ws[1:] {
		if sw.Start != ws[0].Start || sw.End != ws[0].End {
			return false
		}
	}
	return true
}

func profilePatch(f *web.Form) (domain.SettingsPatch, error) {
	var v domain.ValidationError
	week, err := strconv.Atoi(f.Get("week_start"))
	if err != nil {
		v.Add("week_start", "choose a day")
	}
	sleep := domain.SleepInput{Enabled: f.Get("sleep") == "1", Windows: []domain.SleepWindowInput{}}
	custom := f.Get("sleep_mode") == "custom"
	for d := range 7 {
		start, end := f.Get("sleep_start"), f.Get("sleep_end")
		if custom {
			if f.Get(sleepKey("on", d)) != "1" {
				continue
			}
			start, end = f.Get(sleepKey("start", d)), f.Get(sleepKey("end", d))
		}
		sleep.Windows = append(sleep.Windows, domain.SleepWindowInput{Weekday: d, Start: start, End: end})
	}
	return domain.SettingsPatch{
		DisplayName:      domain.Some(f.Get("display_name")),
		Timezone:         domain.Some(f.Get("timezone")),
		WeekStart:        domain.Some(week),
		TimeFormat:       domain.Some(f.Get("time_format")),
		DefaultView:      domain.Some(f.Get("default_view")),
		DedupPolicy:      domain.Some(f.Get("dedup_policy")),
		DateOnlyReminder: domain.Some(f.Get("date_only_reminder")),
		Sleep:            domain.Some(sleep),
	}, v.Err()
}
