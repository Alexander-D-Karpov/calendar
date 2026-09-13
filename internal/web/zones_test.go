package web

import "testing"

func TestZonesAreAllLoadable(t *testing.T) {
	n := 0
	for _, g := range Zones("") {
		if g.Region == "" {
			t.Error("group with no region")
		}
		for _, z := range g.Zones {
			n++
			if z.Name == "" || z.Label == "" {
				t.Errorf("empty zone in %q: %+v", g.Region, z)
			}
		}
	}
	if n < 80 {
		t.Errorf("only %d zones survived validation, the list looks broken", n)
	}
}

// An account may hold a zone the curated list does not name. Dropping it would
// silently rewrite the user's timezone on the next save.
func TestZonesKeepAnUnlistedCurrentZone(t *testing.T) {
	groups := Zones("Antarctica/Troll")
	if groups[0].Region != "Current" {
		t.Fatalf("first group = %q, want Current", groups[0].Region)
	}
	if got := groups[0].Zones[0].Name; got != "Antarctica/Troll" {
		t.Errorf("zone = %q, want Antarctica/Troll", got)
	}
}

func TestZonesDoNotDuplicateAListedCurrentZone(t *testing.T) {
	for _, g := range Zones("Europe/Moscow") {
		if g.Region == "Current" {
			t.Error("a listed zone must not get its own Current group")
		}
	}
}

func TestZoneLabelDropsTheRegion(t *testing.T) {
	cases := map[string]string{
		"Europe/Moscow":                  "Moscow",
		"America/New_York":               "New York",
		"America/Argentina/Buenos_Aires": "Buenos Aires, Argentina",
		"UTC":                            "UTC",
	}
	for name, want := range cases {
		if got := zoneLabel(name); got != want {
			t.Errorf("zoneLabel(%q) = %q, want %q", name, got, want)
		}
	}
}
