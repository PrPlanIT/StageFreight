package version

import "testing"

func TestFixFor(t *testing.T) {
	twoLines := [][]OSVEvent{
		{{Introduced: "2.0.0"}, {Fixed: "2.4.0"}},
		{{Introduced: "0"}, {Fixed: "1.20.9"}},
	}
	oneRangeTwoLines := [][]OSVEvent{{{Introduced: "2.0.0"}, {Fixed: "2.4.0"}, {Introduced: "0"}, {Fixed: "1.20.9"}}}
	cases := []struct {
		name      string
		installed string
		ranges    [][]OSVEvent
		want      string
	}{
		{"1.x install gets its 1.x fix", "1.20.8", twoLines, "1.20.9"},
		{"2.x install gets its 2.x fix", "2.3.1", twoLines, "2.4.0"},
		{"interval order within one range does not matter", "1.20.8", oneRangeTwoLines, "1.20.9"},
		{"single line", "0.4.1", [][]OSVEvent{{{Introduced: "0"}, {Fixed: "0.5.1"}}}, "0.5.1"},
		{"no fix published", "1.4.0", [][]OSVEvent{{{Introduced: "0"}}}, ""},
		{"outside every interval falls back to the lowest fix above", "1.21.0", [][]OSVEvent{{{Introduced: "2.0.0"}, {Fixed: "2.4.0"}}}, "2.4.0"},
		{"already fixed", "2.5.0", twoLines, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FixFor(c.installed, "npm", c.ranges); got != c.want {
				t.Fatalf("FixFor(%s) = %q, want %q", c.installed, got, c.want)
			}
		})
	}
}
