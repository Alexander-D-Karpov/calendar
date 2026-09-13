package realtime

import (
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

var t0 = time.Date(2026, 9, 10, 7, 0, 0, 0, time.UTC)

func tp(t time.Time) *time.Time {
	return &t
}

func TestParsePayload(t *testing.T) {
	owner, id, cal := domain.NewID(), domain.NewID(), domain.NewID()
	raw := `{"seq":42,"owner":"` + owner.String() + `","entity":"event","id":"` + id.String() + `","op":"update","calendar":"` +
		cal.String() + `","list":null,"from":"2026-09-10T07:00:00+00:00","to":null,"origin":null}`
	c, err := ParsePayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	if c.Seq != 42 || c.OwnerID != owner || c.EntityID != id || *c.CalendarID != cal || c.ListID != nil || !c.From.Equal(t0) || c.To != nil {
		t.Fatalf("change = %+v", c)
	}
	for _, bad := range []string{"", "{}", `{"seq":1}`, "nope"} {
		if _, err := ParsePayload(bad); err == nil {
			t.Errorf("ParsePayload(%q) accepted", bad)
		}
	}
}

func TestOverlaps(t *testing.T) {
	from, to := t0, t0.Add(24*time.Hour)
	cases := []struct {
		a, b *time.Time
		want bool
	}{
		{nil, nil, false},
		{tp(t0.Add(time.Hour)), tp(t0.Add(2 * time.Hour)), true},
		{tp(t0.Add(-2 * time.Hour)), tp(t0.Add(-time.Hour)), false},
		{tp(t0.Add(-time.Hour)), nil, true},
		{tp(to), nil, false},
		{tp(t0), tp(t0), true},
	}
	for i, c := range cases {
		if got := Overlaps(domain.Change{From: c.a, To: c.b}, from, to); got != c.want {
			t.Errorf("case %d = %v", i, got)
		}
	}
}

func TestFilters(t *testing.T) {
	cal, other, share := domain.NewID(), domain.NewID(), domain.NewID()
	from, to := t0, t0.Add(7*24*time.Hour)
	in := tp(t0.Add(time.Hour))
	rf := RangeFilter(from, to)
	if rf(domain.Change{Entity: domain.EntityEvent, From: in, To: in}) != Refresh || rf(domain.Change{Entity: domain.EntityTodo}) != Skip || rf(domain.Change{Entity: domain.EntityCalendar}) != Refresh {
		t.Fatal("range filter mismatch")
	}
	sf := ShareFilter(share, []domain.ID{cal}, false, from, to)
	cases := map[Action]domain.Change{
		Reload:  {Entity: domain.EntityShare, EntityID: share},
		Refresh: {Entity: domain.EntityEvent, CalendarID: &cal, From: in, To: in},
		Skip:    {Entity: domain.EntityEvent, CalendarID: &other, From: in, To: in},
	}
	for want, c := range cases {
		if got := sf(c); got != want {
			t.Errorf("share filter %+v = %v, want %v", c, got, want)
		}
	}
	if sf(domain.Change{Entity: domain.EntityTodo, From: in, To: in}) != Skip || sf(domain.Change{Entity: domain.EntityShare, EntityID: other}) != Skip {
		t.Fatal("share filter must skip todos and other shares")
	}
}

func TestHub(t *testing.T) {
	h := NewHub(nil)
	a, b := domain.NewID(), domain.NewID()
	sa, sb := h.Subscribe(a), h.Subscribe(b)
	h.Publish(domain.Change{Seq: 1, OwnerID: a})
	if c := <-sa.C; c.Seq != 1 {
		t.Fatalf("seq = %d", c.Seq)
	}
	select {
	case <-sb.C:
		t.Fatal("other owner must not receive")
	default:
	}
	for i := range subBuffer + 5 {
		h.Publish(domain.Change{Seq: int64(i + 2), OwnerID: a})
	}
	if !sa.Lost() || sa.Lost() {
		t.Fatal("overflow must mark lost once")
	}
	h.Unsubscribe(sb)
	if h.Len() != 1 {
		t.Fatalf("Len = %d", h.Len())
	}
	for range subBuffer {
		<-sa.C
	}
	h.Resync()
	if c := <-sa.C; c.Seq != 0 || !sa.Lost() {
		t.Fatal("resync must wake subscribers")
	}
	h.Close()
	for range sa.C {
	}
	if _, ok := <-h.Subscribe(a).C; ok {
		t.Fatal("subscribe after close must return a closed channel")
	}
}

func TestSubscribeCap(t *testing.T) {
	h := NewHub(nil)
	owner := domain.NewID()
	var subs []*Sub
	for range maxSubsPerOwner {
		s := h.Subscribe(owner)
		if s.Refused() {
			t.Fatal("a subscription under the cap must be admitted")
		}
		subs = append(subs, s)
	}
	extra := h.Subscribe(owner)
	if !extra.Refused() {
		t.Fatal("past the cap the subscription must be refused")
	}
	select {
	case _, ok := <-extra.C:
		if ok {
			t.Fatal("a refused subscription must have closed channels")
		}
	default:
		t.Fatal("a refused subscription must have closed channels")
	}
	// Another account is unaffected, and freeing a slot lets the owner back in.
	if other := h.Subscribe(domain.NewID()); other.Refused() {
		t.Fatal("the cap is per owner")
	}
	h.Unsubscribe(subs[0])
	if again := h.Subscribe(owner); again.Refused() {
		t.Fatal("closing a socket must free a slot")
	}
}
