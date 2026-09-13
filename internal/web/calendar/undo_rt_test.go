package calendar

import (
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

// The undo path stores formValues output and replays it through eventPatch, so
// anything the pair drops is silently lost on every "Event moved" undo.
func TestUndoSnapshotRoundTrip(t *testing.T) {
	start := time.Date(2026, 9, 10, 7, 0, 0, 0, time.UTC)
	e := domain.Event{
		ID: domain.NewID(), CalendarID: domain.NewID(), Title: "Standup", Location: "Office",
		Start: start, End: start.Add(15 * time.Minute), TZ: "Europe/Moscow",
		RRule: "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR", Status: domain.StatusTentative,
		Visibility: domain.VisibilityPrivate, Transparency: domain.TransparencyTransparent,
		Reminders: []int{10, 60},
	}
	snap := snapshot(e, domain.ScopeAll, "")
	p, err := eventPatch(web.NewForm(snap.Values), true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Timezone.V != "Europe/Moscow" {
		t.Errorf("timezone lost: %q", p.Timezone.V)
	}
	if p.Visibility.V != domain.VisibilityPrivate {
		t.Errorf("privacy lost: %q", p.Visibility.V)
	}
	if p.Transparency.V != domain.TransparencyTransparent {
		t.Errorf("free/busy lost: %q", p.Transparency.V)
	}
	if p.Status.V != domain.StatusTentative {
		t.Errorf("status lost: %q", p.Status.V)
	}
	if len(p.Reminders.V) != 2 || p.Reminders.V[0] != 10 || p.Reminders.V[1] != 60 {
		t.Errorf("reminders lost: %v", p.Reminders.V)
	}
	if p.RRule.V != e.RRule {
		t.Errorf("recurrence lost: %q", p.RRule.V)
	}
	if p.Title.V != "Standup" || p.Location.V != "Office" {
		t.Errorf("text lost: %+v", p)
	}
}
