package shares

import (
	"bytes"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/view"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

func TestPagesRender(t *testing.T) {
	r, err := web.EmbeddedRenderer()
	if err != nil {
		t.Fatal(err)
	}
	user := domain.User{ID: domain.NewID(), Email: "sasha@akarpov.ru"}
	day := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	sh := domain.Share{ID: domain.NewID(), Name: "Week", View: "week", Period: day, Detail: domain.DetailTitles, Version: 1}
	p := view.NewPeriod(view.Week, day, time.Monday)
	start := day.Add(9 * time.Hour)
	items := view.Redact([]view.Item{
		{ID: "a", Title: "Standup", Color: "3b6ea5", Start: start, End: start.Add(time.Hour)},
		{ID: "t", Title: "Pay", Todo: true, Color: "5b7c5a", AllDay: true, Start: day, End: day.AddDate(0, 0, 1)},
	}, domain.DetailBusy)
	rendered := view.Render(p, items, view.Options{Loc: time.UTC, Now: start, Clock24: true})
	pages := map[string]any{
		"settings/shares": indexView{Rows: []shareRow{{Share: sh, URL: "https://calendar.test/s/tok", Summary: summary(sh, time.Monday), DetailLabel: "Titles"}}},
		"settings/share_edit": formView{Action: listPath, Cancel: listPath, IsNew: true, Views: viewOptions(), Details: detailOptions, TZModes: tzOptions,
			Calendars: []calOption{{ID: "x", Name: "Work", Checked: true}}},
		"share": publicView{Rendered: rendered, Name: "Week", Title: p.Title(), Kind: "Week", TZ: "UTC", Live: "/s/tok/ws", Seq: 7, Colors: view.Colors(items, nil)},
	}
	for name, data := range pages {
		page := &web.Page{Title: name, Nav: "settings/shares", User: &user, CSRF: "tok", Form: web.NewForm(url.Values{"name": {"Week"}, "cal": {"x"}}), Data: data, Footer: &web.Footer{AppName: "Calendar"}}
		var buf bytes.Buffer
		if err := r.Execute(&buf, name, page); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out := buf.String()
		if strings.Contains(out, "style=") {
			t.Errorf("%s: inline style", name)
		}
		if name != "share" {
			continue
		}
		for _, want := range []string{`data-live="/s/tok/ws"`, `data-seq="7"`, "noindex", "Busy", "Version: "} {
			if !strings.Contains(out, want) {
				t.Errorf("share page missing %q", want)
			}
		}
		for _, bad := range []string{`href="/events/`, `href="/todos/`, "data-toggle", "data-dialog", "Standup", "sasha@akarpov.ru"} {
			if strings.Contains(out, bad) {
				t.Errorf("share page leaks %q", bad)
			}
		}
	}
}
