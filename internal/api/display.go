package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/service"
	"github.com/Alexander-D-Karpov/calendar/internal/store"
)

const (
	defaultDisplayDays = 2
	maxDisplayDays     = 7
	displayOverdue     = 14
	maxDisplayEvents   = 50
	maxDisplayTodos    = 30
	displayTitleLen    = 64
	displayPlaceLen    = 40
)

type displaySleepJSON struct {
	Weekday int `json:"wd"`
	Start   int `json:"s"`
	End     int `json:"e"`
}

type displayWeatherJSON struct {
	Temp float64 `json:"t"`
	Code int     `json:"code"`
	Day  bool    `json:"day"`
	At   int64   `json:"at"`
}

type displayEventJSON struct {
	Start  int64  `json:"s"`
	End    int64  `json:"e"`
	Color  string `json:"c"`
	AllDay bool   `json:"all_day"`
	Title  string `json:"title"`
	Place  string `json:"place"`
}

type displayTodoJSON struct {
	Due      string `json:"due"`
	Time     *int   `json:"time"`
	Priority int    `json:"prio"`
	Done     int    `json:"done"`
	Total    int    `json:"total"`
	Title    string `json:"title"`
}

type displayJSON struct {
	ETag     string              `json:"etag"`
	Now      int64               `json:"now"`
	TZOffset int                 `json:"tz_offset"`
	Sleep    []displaySleepJSON  `json:"sleep"`
	Weather  *displayWeatherJSON `json:"weather"`
	Events   []displayEventJSON  `json:"events"`
	Todos    []displayTodoJSON   `json:"todos"`
}

// The tag leaves out now so that an unchanged screen answers 304.
func (d *displayJSON) tag() string {
	now := d.Now
	d.Now, d.ETag = 0, ""
	b, _ := json.Marshal(d)
	d.Now = now
	sum := sha256.Sum256(b)
	return base64.RawURLEncoding.EncodeToString(sum[:9])
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

func displayCoord(r *http.Request, v *domain.ValidationError, key string, limit float64) (float64, bool) {
	s := strings.TrimSpace(r.URL.Query().Get(key))
	if s == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < -limit || f > limit {
		v.Addf(key, "must be a number between %g and %g", -limit, limit)
		return 0, false
	}
	return f, true
}

func displayEvent(o domain.Occurrence, colors map[domain.ID]string, loc *time.Location) displayEventJSON {
	e := o.Event
	s, end := e.Start, e.End
	if e.AllDay {
		s = time.Date(s.Year(), s.Month(), s.Day(), 0, 0, 0, 0, loc)
		end = time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, loc)
	}
	c := e.Color
	if c == "" {
		c = colors[e.CalendarID]
	}
	return displayEventJSON{
		Start: s.Unix(), End: end.Unix(), Color: c, AllDay: e.AllDay,
		Title: clip(e.Title, displayTitleLen), Place: clip(e.Location, displayPlaceLen),
	}
}

func displayTodo(t domain.Todo) displayTodoJSON {
	out := displayTodoJSON{Due: t.DueDate.Format(domain.DateLayout), Time: t.DueTime, Priority: t.Priority, Total: len(t.Checks), Title: clip(t.Title, displayTitleLen)}
	for _, c := range t.Checks {
		if c.Done {
			out.Done++
		}
	}
	return out
}

func getDisplay(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		var v domain.ValidationError
		days, err := queryInt(r.URL.Query(), "days", defaultDisplayDays)
		if err != nil {
			return err
		}
		if days < 1 || days > maxDisplayDays {
			v.Addf("days", "must be between 1 and %d", maxDisplayDays)
		}
		lat, hasLat := displayCoord(r, &v, "lat", 90)
		lon, hasLon := displayCoord(r, &v, "lon", 180)
		if hasLat != hasLon && v.Err() == nil {
			v.Add("lat", "lat and lon go together")
		}
		if err := v.Err(); err != nil {
			return err
		}

		ctx, owner := r.Context(), ownerID(r)
		u, ws, err := d.Settings.Get(ctx, owner)
		if err != nil {
			return err
		}
		loc, err := time.LoadLocation(u.Timezone)
		if err != nil {
			loc = time.UTC
		}
		now := d.Clock.Now().In(loc)
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
		end := today.AddDate(0, 0, days)

		cals, err := d.Calendars.List(ctx, owner)
		if err != nil {
			return err
		}
		colors := make(map[domain.ID]string, len(cals))
		for _, c := range cals {
			colors[c.ID] = c.Color
		}
		occ, err := d.Events.List(ctx, owner, service.EventQuery{From: today.Format(time.RFC3339), To: end.Format(time.RFC3339), Expand: true})
		if err != nil {
			return err
		}

		dayUTC := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
		from, to := dayUTC.AddDate(0, 0, -displayOverdue), dayUTC.AddDate(0, 0, days)
		todos, err := d.Todos.Find(ctx, owner, store.TodoFilter{
			TopLevel: true, Status: domain.TodoOpen, Scheduled: true,
			DueFrom: &from, DueTo: &to, Order: store.OrderByDue, Limit: maxDisplayTodos,
		})
		if err != nil {
			return err
		}

		_, offset := now.Zone()
		out := displayJSON{
			Now: now.Unix(), TZOffset: offset,
			Sleep: []displaySleepJSON{}, Events: []displayEventJSON{}, Todos: []displayTodoJSON{},
		}
		if u.SleepEnabled {
			for _, sw := range ws {
				out.Sleep = append(out.Sleep, displaySleepJSON{Weekday: int(sw.Weekday), Start: sw.Start, End: sw.End})
			}
		}
		for _, o := range occ[:min(len(occ), maxDisplayEvents)] {
			out.Events = append(out.Events, displayEvent(o, colors, loc))
		}
		for _, t := range todos {
			out.Todos = append(out.Todos, displayTodo(t))
		}
		if hasLat && d.Weather != nil {
			if c := d.Weather.Current(ctx, lat, lon); c != nil {
				out.Weather = &displayWeatherJSON{Temp: c.Temp, Code: c.Code, Day: c.Day, At: c.At.Unix()}
			}
		}
		out.ETag = out.tag()
		writeResource(w, r, http.StatusOK, `"`+out.ETag+`"`, out)
		return nil
	}
}
