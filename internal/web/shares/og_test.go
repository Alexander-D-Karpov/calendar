package shares

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/view"
)

func TestCountsOf(t *testing.T) {
	cases := []struct {
		items []view.Item
		want  string
	}{
		{nil, "0 events"},
		{[]view.Item{{ID: "a"}}, "1 event"},
		{[]view.Item{{ID: "a"}, {ID: "b"}}, "2 events"},
		{[]view.Item{{ID: "a"}, {ID: "t", Todo: true}}, "1 event, 1 todo"},
		{[]view.Item{{ID: "t", Todo: true}, {ID: "u", Todo: true}}, "0 events, 2 todos"},
	}
	for _, c := range cases {
		if got := countsOf(c.items); got != c.want {
			t.Errorf("countsOf(%d items) = %q, want %q", len(c.items), got, c.want)
		}
	}
}

// The image URL is served immutable, so the version has to move whenever either
// the share itself or anything it draws changes.
func TestOGVersionTracksBothInputs(t *testing.T) {
	m := &Module{stamps: stubStamp(7)}
	sh := domain.Share{OwnerID: domain.NewID(), Version: 3}
	base := m.ogVersion(t.Context(), sh)
	if !strings.Contains(base, "-") {
		t.Fatalf("version = %q, want share and stamp parts", base)
	}
	bumped := sh
	bumped.Version = 4
	if m.ogVersion(t.Context(), bumped) == base {
		t.Error("editing the share must change the version")
	}
	m.stamps = stubStamp(8)
	if m.ogVersion(t.Context(), sh) == base {
		t.Error("a change to what it draws must change the version")
	}
	if want := strconv.FormatInt(3, 36) + "-" + strconv.FormatInt(8, 36); m.ogVersion(t.Context(), sh) != want {
		t.Errorf("version = %q, want %q", m.ogVersion(t.Context(), sh), want)
	}
}

func TestOGVersionWithoutAStampStore(t *testing.T) {
	m := &Module{}
	got := m.ogVersion(t.Context(), domain.Share{Version: 3})
	if got != strconv.FormatInt(3, 36) {
		t.Errorf("version = %q, want the share version alone", got)
	}
}

type stubStamp int64

func (s stubStamp) ShareStamp(_ context.Context, _ domain.ID, _ []domain.ID, _ bool) (int64, error) {
	return int64(s), nil
}
