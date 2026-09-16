package main

import (
	"testing"

	"github.com/Alexander-D-Karpov/calendar/internal/dedup"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

// UUIDv7 sorts by creation time, so the smaller string is the older event.
var older, newer = mustID("01a09ea8-5af2-7e04-89ae-1a0b4aff1b91"), mustID("01a0aad6-0f37-7d8e-9722-ae2c919a2ce8")

func mustID(s string) domain.ID {
	id, err := domain.ParseID(s)
	if err != nil {
		panic(err)
	}
	return id
}

func TestChooseKeepsTheCopyGoogleIsLinkedTo(t *testing.T) {
	d := domain.Duplicate{AID: older, BID: newer}

	// Dropping the mapped copy would strand the Google event with nothing
	// pointing at it, and no later sync could repair that.
	if got, _ := choose(true, false, d); got != dedup.ActionKeepA {
		t.Fatalf("A mapped: got %s, want %s", got, dedup.ActionKeepA)
	}
	if got, _ := choose(false, true, d); got != dedup.ActionKeepB {
		t.Fatalf("B mapped: got %s, want %s", got, dedup.ActionKeepB)
	}
}

func TestChooseKeepsTheMappedCopyEvenWhenItIsTheNewerOne(t *testing.T) {
	// The re-imported copy is the newer one and it owns the mapping, so the
	// remote link has to win over age.
	d := domain.Duplicate{AID: older, BID: newer}
	got, why := choose(false, true, d)
	if got != dedup.ActionKeepB {
		t.Fatalf("got %s (%s), want %s", got, why, dedup.ActionKeepB)
	}
}

func TestChooseFallsBackToTheOriginalWhenNeitherIsLinked(t *testing.T) {
	if got, _ := choose(false, false, domain.Duplicate{AID: older, BID: newer}); got != dedup.ActionKeepA {
		t.Fatalf("A older: got %s, want %s", got, dedup.ActionKeepA)
	}
	if got, _ := choose(false, false, domain.Duplicate{AID: newer, BID: older}); got != dedup.ActionKeepB {
		t.Fatalf("B older: got %s, want %s", got, dedup.ActionKeepB)
	}
}

func TestChooseFallsBackToTheOriginalWhenBothAreLinked(t *testing.T) {
	if got, _ := choose(true, true, domain.Duplicate{AID: older, BID: newer}); got != dedup.ActionKeepA {
		t.Fatalf("got %s, want %s", got, dedup.ActionKeepA)
	}
}

func TestChooseAlwaysReturnsAValidAction(t *testing.T) {
	for _, tc := range [][2]bool{{false, false}, {true, false}, {false, true}, {true, true}} {
		got, why := choose(tc[0], tc[1], domain.Duplicate{AID: older, BID: newer})
		if got != dedup.ActionKeepA && got != dedup.ActionKeepB {
			t.Fatalf("choose(%v) = %q, not a keep action", tc, got)
		}
		if why == "" {
			t.Fatalf("choose(%v) gave no reason", tc)
		}
	}
}
