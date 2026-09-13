package web

import (
	"sort"
	"strings"
	"sync"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

// The container image is distroless and carries no /usr/share/zoneinfo, so the
// list cannot be read from disk. time/tzdata is linked in for LoadLocation but
// exposes no way to enumerate, hence this curated set. Any zone missing here is
// still accepted: whatever the account already holds is added to the list.
var zoneNames = []string{
	"UTC",

	"Europe/London", "Europe/Dublin", "Europe/Lisbon", "Europe/Madrid", "Europe/Paris",
	"Europe/Brussels", "Europe/Amsterdam", "Europe/Berlin", "Europe/Zurich", "Europe/Rome",
	"Europe/Vienna", "Europe/Prague", "Europe/Warsaw", "Europe/Budapest", "Europe/Belgrade",
	"Europe/Stockholm", "Europe/Oslo", "Europe/Copenhagen", "Europe/Helsinki", "Europe/Tallinn",
	"Europe/Riga", "Europe/Vilnius", "Europe/Kyiv", "Europe/Chisinau", "Europe/Bucharest",
	"Europe/Sofia", "Europe/Athens", "Europe/Istanbul", "Europe/Minsk", "Europe/Moscow",
	"Europe/Samara",

	"America/St_Johns", "America/Halifax", "America/New_York", "America/Toronto",
	"America/Detroit", "America/Chicago", "America/Winnipeg", "America/Mexico_City",
	"America/Denver", "America/Edmonton", "America/Phoenix", "America/Los_Angeles",
	"America/Vancouver", "America/Anchorage", "Pacific/Honolulu",
	"America/Bogota", "America/Lima", "America/Caracas", "America/Santiago",
	"America/Sao_Paulo", "America/Argentina/Buenos_Aires", "America/Montevideo",

	"Africa/Casablanca", "Africa/Lagos", "Africa/Algiers", "Africa/Cairo",
	"Africa/Johannesburg", "Africa/Nairobi", "Africa/Accra", "Africa/Tunis",

	"Asia/Jerusalem", "Asia/Beirut", "Asia/Amman", "Asia/Baghdad", "Asia/Riyadh",
	"Asia/Dubai", "Asia/Tehran", "Asia/Baku", "Asia/Tbilisi", "Asia/Yerevan",
	"Asia/Karachi", "Asia/Tashkent", "Asia/Kolkata", "Asia/Colombo", "Asia/Kathmandu",
	"Asia/Dhaka", "Asia/Almaty", "Asia/Yekaterinburg", "Asia/Novosibirsk",
	"Asia/Krasnoyarsk", "Asia/Irkutsk", "Asia/Yakutsk", "Asia/Vladivostok",
	"Asia/Bangkok", "Asia/Jakarta", "Asia/Ho_Chi_Minh", "Asia/Singapore",
	"Asia/Kuala_Lumpur", "Asia/Manila", "Asia/Hong_Kong", "Asia/Shanghai",
	"Asia/Taipei", "Asia/Seoul", "Asia/Tokyo",

	"Australia/Perth", "Australia/Adelaide", "Australia/Brisbane", "Australia/Sydney",
	"Australia/Melbourne", "Australia/Hobart", "Australia/Darwin",
	"Pacific/Auckland", "Pacific/Fiji", "Pacific/Guam",
}

// Zone is one option in the timezone picker.
type Zone struct {
	Name  string
	Label string
}

// ZoneGroup is an optgroup: the region before the slash, with UTC on its own.
type ZoneGroup struct {
	Region string
	Zones  []Zone
}

var validZones = sync.OnceValue(func() []string {
	out := make([]string, 0, len(zoneNames))
	for _, name := range zoneNames {
		if name == "UTC" || domain.ValidTimezone(name) {
			out = append(out, name)
		}
	}
	return out
})

// Zones returns the picker options, grouped by region, with current selected
// first in its own group when it is not already one of the listed zones.
func Zones(current string) []ZoneGroup {
	current = strings.TrimSpace(current)
	names := validZones()
	known := make(map[string]bool, len(names))
	for _, n := range names {
		known[n] = true
	}

	var groups []ZoneGroup
	if current != "" && !known[current] {
		groups = append(groups, ZoneGroup{
			Region: "Current",
			Zones:  []Zone{{Name: current, Label: zoneLabel(current)}},
		})
	}

	byRegion := map[string][]Zone{}
	var order []string
	for _, n := range names {
		region, _, ok := strings.Cut(n, "/")
		if !ok {
			region = "Universal"
		}
		if _, seen := byRegion[region]; !seen {
			order = append(order, region)
		}
		byRegion[region] = append(byRegion[region], Zone{Name: n, Label: zoneLabel(n)})
	}
	sort.Strings(order)
	for _, region := range order {
		zones := byRegion[region]
		sort.Slice(zones, func(i, j int) bool { return zones[i].Label < zones[j].Label })
		groups = append(groups, ZoneGroup{Region: region, Zones: zones})
	}
	return groups
}

// zoneLabel drops the region and makes the city readable: the select already
// groups by region, so repeating it in every option is noise.
func zoneLabel(name string) string {
	_, city, ok := strings.Cut(name, "/")
	if !ok {
		return name
	}
	city = strings.ReplaceAll(city, "_", " ")
	if region, sub, nested := strings.Cut(city, "/"); nested {
		city = strings.ReplaceAll(sub, "_", " ") + ", " + region
	}
	return city
}
