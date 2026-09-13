package web

import (
	"bytes"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/buildinfo"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/view"
)

func testRenderer(t *testing.T) *Renderer {
	t.Helper()
	r, err := EmbeddedRenderer()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func samplePage(user bool) *Page {
	p := &Page{
		Title:            "Test",
		CSRF:             "csrf-value",
		Theme:            "dark",
		Form:             NewForm(nil),
		Footer:           newFooter("Calendar", buildinfo.Info{Version: "v1.2.3", Commit: "0123456789abcdef", GoVersion: "go1.27.0", Date: "2026-09-10T12:00:00Z"}),
		started:          time.Now(),
		tplStart:         time.Now(),
		loc:              time.UTC,
		RegistrationOpen: true,
	}
	if user {
		p.User = &domain.User{ID: domain.NewID(), Email: "sasha@akarpov.ru", DisplayName: "Sasha", Timezone: "UTC", PasswordHash: "x"}
	}
	return p
}

func TestPagesRender(t *testing.T) {
	r := testRenderer(t)
	now := time.Now()
	cases := map[string]any{
		"login":    nil,
		"register": map[string]any{"MinPassword": 10},
		"reauth":   map[string]any{"Password": true, "Google": true},
		"settings/security": map[string]any{
			"HasPassword": true, "Fresh": true, "GoogleOn": true, "MinPassword": 10,
			"Google": &domain.Identity{Email: "sasha@gmail.com", LinkedAt: now},
		},
		"verify": map[string]any{"Message": "This link expired or was already used.", "Resend": true},
		"forgot": nil,
		"reset":  nil,
		"error":  errorView{Status: 404, Title: "Not Found", Detail: "gone"},
		"settings/sessions": []map[string]any{
			{"ID": domain.NewID(), "UserAgent": "Mozilla/5.0 (X11; Linux x86_64) Firefox/130.0", "IP": netip.MustParseAddr("198.51.100.7"), "CreatedAt": now, "LastSeenAt": now, "Current": true},
			{"ID": domain.NewID(), "UserAgent": "", "IP": netip.Addr{}, "CreatedAt": now, "LastSeenAt": now, "Current": false},
		},
		"settings/tokens": map[string]any{
			"Tokens": []domain.APIToken{{ID: domain.NewID(), Name: "cli", Prefix: "abcdefghijkl", Scopes: []string{"todos:read"}, CreatedAt: now}},
			"Scopes": auth.ScopeStrings(auth.AllScopes),
			"Expiry": []map[string]string{{"Value": "30", "Label": "30 days"}, {"Value": "", "Label": "Never"}},
			"Now":    now,
			"Fresh":  true,
		},
		"settings/token_created": map[string]any{"Token": "cal_x_y", "Info": domain.APIToken{Name: "cli", Scopes: []string{"todos:read"}}, "Origin": "https://calendar.test"},
	}
	common := []string{
		"<!doctype html>",
		`data-theme="dark"`,
		"Page: ",
		"Template: ",
		`href="/api/docs/"`,
		repoURL,
		"/static/css/base.css?v=",
		"/releases/tag/v1.2.3",
		"/commit/0123456789abcdef",
		"Built: 2026-09-10",
		"Go 1.27.0",
	}
	for name, data := range cases {
		p := samplePage(true)
		p.Data = data
		var buf bytes.Buffer
		if err := r.Execute(&buf, name, p); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		out := buf.String()
		for _, want := range common {
			if !strings.Contains(out, want) {
				t.Errorf("%s: missing %q", name, want)
			}
		}
	}
}

func TestPageSpecifics(t *testing.T) {
	r := testRenderer(t)
	render := func(name string, p *Page) string {
		t.Helper()
		var buf bytes.Buffer
		if err := r.Execute(&buf, name, p); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return buf.String()
	}

	anon := samplePage(false)
	anon.Form.Errors["email"] = "Must be valid."
	anon.Form.Error = "Incorrect email or password."
	out := render("login", anon)
	for _, want := range []string{`action="/login"`, "Incorrect email or password.", `href="/register"`, `name="csrf_token" value="csrf-value"`} {
		if !strings.Contains(out, want) {
			t.Errorf("login missing %q", want)
		}
	}
	if strings.Contains(out, "Sign out") {
		t.Error("anonymous page must not show sign out")
	}

	p := samplePage(true)
	p.Data = map[string]any{"Token": "cal_abc_secret", "Info": domain.APIToken{Name: "cli"}, "Origin": "https://calendar.test"}
	out = render("settings/token_created", p)
	if !strings.Contains(out, `value="cal_abc_secret"`) || !strings.Contains(out, "Sign out") {
		t.Error("token page must show the token and the user header")
	}

	if err := r.Execute(&bytes.Buffer{}, "nope", p); err == nil {
		t.Error("unknown page must fail")
	}
}

func TestCalendarPagesRender(t *testing.T) {
	r := testRenderer(t)
	start := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	items := []view.Item{
		{ID: "a", Title: "Standup", Color: "3b6ea5", Start: start, End: start.Add(15 * time.Minute)},
		{ID: "b", Title: "Offsite", Color: "aa3355", AllDay: true, Start: view.Date(start), End: view.Date(start).AddDate(0, 0, 2)},
		{ID: "t", Title: "Pay rent", Color: "5b7c5a", Todo: true, AllDay: true, Start: view.Date(start), End: view.Date(start).AddDate(0, 0, 1)},
	}
	o := view.Options{Loc: time.UTC, Now: start, Clock24: true, Sleep: []domain.SleepWindow{{Weekday: time.Thursday, Start: 23 * 60, End: 7 * 60}}}
	for _, k := range view.Kinds {
		p := view.NewPeriod(k, start, time.Monday)
		data := map[string]any{"Title": p.Title(), "Colors": []string{"3b6ea5", "aa3355"}}
		switch k {
		case view.Month:
			data["Month"] = view.BuildMonth(p, items, o)
		case view.Year:
			data["Year"] = view.BuildYear(p, items, o)
		case view.Agenda:
			data["Agenda"] = view.BuildAgenda(p, items, o)
		default:
			data["Grid"] = view.BuildGrid(p, items, o)
		}
		page := samplePage(true)
		page.Data = data
		var buf bytes.Buffer
		if err := r.Execute(&buf, "calendar", page); err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		out := buf.String()
		if k != view.Year && !strings.Contains(out, "Standup") {
			t.Errorf("%s: missing event", k)
		}
		if k != view.Year && !strings.Contains(out, `data-toggle="/todos/t/toggle"`) {
			t.Errorf("%s: missing todo toggle", k)
		}
		if !strings.Contains(out, "colors.css?c=3b6ea5") || strings.Contains(out, "style=") {
			t.Errorf("%s: colors link or inline style mismatch", k)
		}
	}

	page := samplePage(true)
	page.Data = map[string]any{
		"Event":    domain.Event{ID: domain.NewID(), Title: "Standup", Body: "**bold** <script>x</script>", Visibility: "default", Status: "confirmed"},
		"Calendar": domain.Calendar{Name: "Work"},
		"When":     "Thursday, 10 September 2026, 09:00 – 09:15",
		"Editable": true,
		"EditHref": "/events/x/edit",
		"Back":     "/cal/week/2026-09-10",
	}
	var buf bytes.Buffer
	if err := r.Execute(&buf, "event", page); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); !strings.Contains(out, "<strong>bold</strong>") || strings.Contains(out, "<script>x") {
		t.Fatal("event body must be rendered and sanitized")
	}

	page = samplePage(true)
	page.Form = NewForm(url.Values{"title": {"Standup"}, "repeat": {"none"}})
	page.Data = map[string]any{
		"Action":    "/events",
		"Cancel":    "/",
		"Calendars": []domain.Calendar{{ID: domain.NewID(), Name: "Work"}},
		"Repeats":   []map[string]string{{"Value": "none", "Label": "Does not repeat"}},
	}
	buf.Reset()
	if err := r.Execute(&buf, "event_edit", page); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); !strings.Contains(out, `action="/events"`) || !strings.Contains(out, `value="Standup"`) {
		t.Fatal("event form mismatch")
	}
}
