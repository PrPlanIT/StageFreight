package version

import "sort"

// OSVEvent is one event of an OSV affected range.
type OSVEvent struct {
	Introduced string
	Fixed      string
}

// FixFor returns the version that fixes installed under OSV affected ranges: the fixed
// event closing the affected interval that contains installed, so a 1.x install is
// pointed at its 1.x fix rather than a later line's. When no interval contains it, the
// lowest fix above it; "" when there is none. Each range is one slice of events.
func FixFor(installed, ecosystem string, ranges [][]OSVEvent) string {
	type event struct {
		ver   string
		fixed bool
	}
	less := func(a, b string) bool {
		if a == "0" {
			return b != "0"
		}
		if b == "0" {
			return false
		}
		return CompareVersions(a, b, ecosystem) < 0
	}
	fallback := ""
	for _, r := range ranges {
		var evs []event
		for _, e := range r {
			if e.Introduced != "" {
				evs = append(evs, event{ver: e.Introduced})
			}
			if e.Fixed != "" {
				evs = append(evs, event{ver: e.Fixed, fixed: true})
			}
		}
		sort.SliceStable(evs, func(i, j int) bool { return less(evs[i].ver, evs[j].ver) })
		intro := ""
		for _, e := range evs {
			if !e.fixed {
				intro = e.ver
				continue
			}
			if intro != "" && !less(installed, intro) && less(installed, e.ver) {
				return e.ver
			}
			intro = ""
			if less(installed, e.ver) && (fallback == "" || less(e.ver, fallback)) {
				fallback = e.ver
			}
		}
	}
	return fallback
}
