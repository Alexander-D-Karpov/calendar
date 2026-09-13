package dedup

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

func TestPairOrdering(t *testing.T) {
	a, b := domain.NewID(), domain.NewID()
	if b.String() < a.String() {
		a, b = b, a
	}
	d1 := duplicate(domain.NewID(), domain.EntityEvent, a, b, domain.ReasonUID, 1, []byte(`{"id":"a"}`), []byte(`{"id":"b"}`))
	d2 := duplicate(domain.NewID(), domain.EntityEvent, b, a, domain.ReasonUID, 1, []byte(`{"id":"b"}`), []byte(`{"id":"a"}`))
	if d1.AID != d2.AID || d1.BID != d2.BID {
		t.Fatal("a pair must order the same regardless of argument order")
	}
	if string(d1.A) != string(d2.A) || string(d1.B) != string(d2.B) {
		t.Fatal("snapshots must follow their ids when swapped")
	}
	if pairKey(a, b) != pairKey(b, a) {
		t.Fatal("pairKey must be symmetric")
	}
}

func TestAutoPolicy(t *testing.T) {
	cases := []struct {
		policy, reason string
		want           bool
	}{
		{domain.DedupOff, domain.ReasonUID, false},
		{domain.DedupFlag, domain.ReasonUID, false},
		{domain.DedupAutoExact, domain.ReasonUID, true},
		{domain.DedupAutoExact, domain.ReasonFingerprint, false},
		{domain.DedupAutoPrint, domain.ReasonFingerprint, true},
		{domain.DedupAutoPrint, domain.ReasonFuzzy, false},
	}
	for _, c := range cases {
		d := domain.Duplicate{Reason: c.reason}
		if got := d.Auto(c.policy); got != c.want {
			t.Errorf("%s + %s = %v, want %v", c.policy, c.reason, got, c.want)
		}
	}
}

func TestSnapshots(t *testing.T) {
	at := time.Date(2026, 9, 7, 7, 0, 0, 0, time.UTC)
	e := domain.Event{ID: domain.NewID(), Title: "Standup", Location: "Office", Start: at, TZ: "Europe/Moscow", RRule: "FREQ=DAILY", Reminders: []int{10}, CreatedAt: at}
	s := Decode(eventSnapshot(e))
	if s.Title != "Standup" || s.When != "2026-09-07 10:00" || len(s.Extra) != 2 {
		t.Fatalf("event snapshot = %+v", s)
	}
	due, tm := at, 18*60
	td := domain.Todo{ID: domain.NewID(), Title: "GPU", DueDate: &due, DueTime: &tm, Checks: []domain.Check{{Text: "x"}}, CreatedAt: at}
	s = Decode(todoSnapshot(td))
	if s.When != "2026-09-07 18:00" || len(s.Extra) != 1 {
		t.Fatalf("todo snapshot = %+v", s)
	}
	if s := Decode([]byte("not json")); s.Title != "" {
		t.Fatal("a broken snapshot must decode to an empty one")
	}
	var raw map[string]any
	if err := json.Unmarshal(eventSnapshot(e), &raw); err != nil {
		t.Fatal(err)
	}
}
