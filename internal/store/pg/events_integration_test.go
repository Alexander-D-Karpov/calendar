//go:build integration

package pg

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/store"
	"github.com/Alexander-D-Karpov/calendar/internal/testutil/pgtest"
)

func TestEventStore(t *testing.T) {
	ctx := context.Background()
	st := New(pgtest.New(t).Pool)
	u, err := st.CreateUser(ctx, domain.NewUser{ID: domain.NewID(), Email: "ev@example.com", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	cals, err := st.ListCalendars(ctx, u.ID)
	if err != nil || len(cals) != 1 {
		t.Fatalf("calendars = %v %v", cals, err)
	}
	cal := cals[0]
	start := time.Date(2026, 9, 7, 7, 0, 0, 0, time.UTC)
	base := domain.Event{
		ID: domain.NewID(), OwnerID: u.ID, CalendarID: cal.ID, UID: "standup", Title: "Standup",
		Start: start, End: start.Add(15 * time.Minute), TZ: "Europe/Moscow", RRule: "FREQ=DAILY",
		Status: domain.StatusConfirmed, Transparency: domain.TransparencyOpaque, Visibility: domain.VisibilityDefault,
		Reminders: []int{10, 30},
	}

	var master, override domain.Event
	err = st.WithEvents(ctx, u.ID, func(tx store.EventTx) error {
		var err error
		if master, err = tx.Insert(ctx, base); err != nil {
			return err
		}
		rid := start.AddDate(0, 0, 1)
		ov := master
		ov.ID, ov.SeriesID, ov.RecurrenceID, ov.RRule = domain.NewID(), &master.ID, &rid, ""
		ov.Start = rid.Add(time.Hour)
		ov.End = ov.Start.Add(15 * time.Minute)
		override, err = tx.Insert(ctx, ov)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if master.Version != 1 || !master.Start.Equal(start) || !slices.Equal(master.Reminders, []int{10, 30}) {
		t.Fatalf("master = %+v", master)
	}

	far := start.AddDate(1, 0, 0)
	rows, err := st.EventsInRange(ctx, u.ID, []domain.ID{cal.ID}, far, far.Add(time.Hour))
	if err != nil || len(rows) != 1 || rows[0].ID != master.ID {
		t.Fatalf("open series range = %v %v", rows, err)
	}
	ovs, err := st.OverridesOf(ctx, u.ID, []domain.ID{master.ID})
	if err != nil || len(ovs) != 1 || !ovs[0].RecurrenceID.Equal(start.AddDate(0, 0, 1)) {
		t.Fatalf("overrides = %v %v", ovs, err)
	}

	err = st.WithEvents(ctx, u.ID, func(tx store.EventTx) error {
		m, err := tx.Get(ctx, master.ID)
		if err != nil {
			return err
		}
		m.Title = "Daily"
		if m, err = tx.Update(ctx, m); err != nil {
			return err
		}
		if m.Version != 2 || m.Title != "Daily" {
			return fmt.Errorf("updated = %+v", m)
		}
		return tx.Delete(ctx, m, time.Now())
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Event(ctx, u.ID, override.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("override after master delete err = %v", err)
	}
	var n int
	if err := st.pool.QueryRow(ctx, `SELECT count(*) FROM changes WHERE owner_id = $1 AND entity = 'event'`, u.ID).Scan(&n); err != nil || n != 4 {
		t.Fatalf("changes = %d %v", n, err)
	}
}
