//go:build integration

package pg

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/service"
	"github.com/Alexander-D-Karpov/calendar/internal/testutil/pgtest"
)

func TestShareStore(t *testing.T) {
	ctx := context.Background()
	st := New(pgtest.New(t).Pool)
	u, err := st.CreateUser(ctx, domain.NewUser{ID: domain.NewID(), Email: "share@example.com", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	cals, err := st.ListCalendars(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.NewKeyRing(map[uint32][]byte{1: bytes.Repeat([]byte{7}, crypto.KeySize)}, 0)
	if err != nil {
		t.Fatal(err)
	}
	svc := service.NewShares(st, st, keys, clock.New())

	sh, err := svc.Create(ctx, u.ID, domain.SharePatch{
		Name:      domain.Some("Week"),
		View:      domain.Some("week"),
		Period:    domain.Some("2026-09-07"),
		Calendars: domain.Some([]string{cals[0].ID.String(), cals[0].ID.String()}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if sh.Token == "" || len(sh.Calendars) != 1 || sh.Version != 1 || !sh.ShowSleep || sh.Period.Format(domain.DateLayout) != "2026-09-07" {
		t.Fatalf("share = %+v", sh)
	}
	opened, err := svc.Open(ctx, sh.Token, true)
	if err != nil || opened.ID != sh.ID {
		t.Fatalf("open = %v %v", opened.ID, err)
	}
	upd, err := svc.Update(ctx, u.ID, sh.ID, domain.SharePatch{Detail: domain.Some(domain.DetailBusy)}, sh.ETag())
	if err != nil || upd.Version != 2 || upd.Detail != domain.DetailBusy || upd.Token != sh.Token {
		t.Fatalf("update = %+v %v", upd, err)
	}
	if _, err := svc.Update(ctx, u.ID, sh.ID, domain.SharePatch{View: domain.Some("day")}, ""); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("view change err = %v", err)
	}
	if _, err := svc.Update(ctx, u.ID, sh.ID, domain.SharePatch{Name: domain.Some("x")}, sh.ETag()); !errors.Is(err, domain.ErrPrecondition) {
		t.Fatalf("stale etag err = %v", err)
	}
	reg, err := svc.Regenerate(ctx, u.ID, sh.ID)
	if err != nil || reg.Token == sh.Token {
		t.Fatalf("regenerate = %v", err)
	}
	if _, err := svc.Open(ctx, sh.Token, false); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("old token err = %v", err)
	}
	if err := svc.Revoke(ctx, u.ID, sh.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Open(ctx, reg.Token, false); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revoked err = %v", err)
	}
	list, err := svc.List(ctx, u.ID)
	if err != nil || len(list) != 1 || list[0].Active() || list[0].AccessCount != 1 {
		t.Fatalf("list = %+v %v", list, err)
	}
	seq, err := st.LatestSeq(ctx, u.ID)
	if err != nil || seq == 0 {
		t.Fatalf("seq = %d %v", seq, err)
	}
	changes, err := st.ChangesSince(ctx, u.ID, 0, 100)
	if err != nil || len(changes) != 4 || changes[0].Entity != domain.EntityShare {
		t.Fatalf("changes = %+v %v", changes, err)
	}
}
