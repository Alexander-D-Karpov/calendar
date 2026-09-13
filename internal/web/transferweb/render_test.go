package transferweb

import (
	"bytes"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/importer"
	"github.com/Alexander-D-Karpov/calendar/internal/subscription"
	"github.com/Alexander-D-Karpov/calendar/internal/transfer"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

func TestPagesRender(t *testing.T) {
	r, err := web.EmbeddedRenderer()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	user := domain.User{ID: domain.NewID(), Email: "sasha@akarpov.ru"}
	im := domain.Import{ID: domain.NewID(), Source: "ics", Filename: "work.ics", Status: domain.ImportPreviewed, CreatedAt: now}
	preview := importer.Preview{
		Source: "ics", Filename: "work.ics", Timezone: "Europe/Moscow",
		Targets:  []importer.Target{{Name: "Work", Kind: domain.BindCalendar, Target: importer.TargetNew, Events: 3}},
		Stats:    importer.Stats{New: 3, Duplicate: 1},
		Samples:  []importer.Sample{{Where: "event 1", Title: "Standup", When: "2026-09-07 10:00", Action: "new"}},
		Problems: []transfer.Problem{{Where: "event 9", Message: "has no valid start"}},
	}
	pages := map[string]any{
		"settings/import": importView{MaxSize: "20MB", History: []importRow{{Import: im, Counts: "3 added"}}},
		"settings/import_preview": previewView{
			Import: im, Preview: preview, CommitTo: "/settings/import/x/commit",
			Targets: []targetRow{{Target: preview.Targets[0], Key: "target_0", SkipKey: "skip_0",
				Options: []web.Option{{Value: importer.TargetNew, Label: "New calendar"}}}},
		},
		"settings/export": exportView{
			Calendars: []domain.Calendar{{ID: domain.NewID(), Name: "Work"}},
			Lists:     []domain.TodoList{{ID: domain.NewID(), Name: "Inbox"}},
		},
		"settings/subscriptions": subsView{MinEvery: "15m0s", Rows: []subscription.Status{{
			Subscription: domain.Subscription{ID: domain.NewID(), Status: domain.SubOK, Interval: time.Hour, Host: "example.com", LastOKAt: &now},
			URL:          "https://example.com/calendar.ics",
			Calendar:     domain.Calendar{Name: "Team"},
		}}},
	}
	for name, data := range pages {
		p := &web.Page{
			Title: name, Nav: "settings/import", User: &user, CSRF: "tok", Data: data,
			Form: web.NewForm(url.Values{"target_0": {importer.TargetNew}, "every": {"1h"}}), Footer: &web.Footer{AppName: "Calendar"},
		}
		var buf bytes.Buffer
		if err := r.Execute(&buf, name, p); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if out := buf.String(); strings.Contains(out, "style=") || !strings.Contains(out, "<!doctype html>") {
			t.Errorf("%s: inline style or missing layout", name)
		}
	}
}
