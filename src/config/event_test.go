package config

import "testing"

func TestEventMatches(t *testing.T) {
	cases := []struct {
		name   string
		events []string
		cur    string
		want   bool
	}{
		{"empty filter passes", nil, "push", true},
		{"empty current is lenient", []string{"tag"}, "", true},
		{"member push", []string{"push"}, "push", true},
		{"member among many", []string{"tag", "schedule"}, "tag", true},
		{"non-member rejected", []string{"tag"}, "push", false},
		{"case-insensitive", []string{"Push"}, "push", true},
		{"whitespace trimmed", []string{" push "}, "push", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := EventMatches(c.events, c.cur); got != c.want {
				t.Errorf("EventMatches(%v, %q) = %v, want %v", c.events, c.cur, got, c.want)
			}
		})
	}
}
