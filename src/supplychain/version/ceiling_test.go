package version

import "testing"

func TestCeilingTarget(t *testing.T) {
	avail := []string{"1.2.3", "1.2.5", "1.2.7", "1.3.0", "1.4.2", "2.0.0"}
	cases := []struct {
		name, current, maxUpdate, want string
	}{
		{"patch picks newest patch of current minor", "1.2.3", "patch", "1.2.7"},
		{"minor picks newest minor below major", "1.2.3", "minor", "1.4.2"},
		{"major picks the newest overall", "1.2.3", "major", "2.0.0"},
		{"nothing newer under patch", "1.2.7", "patch", ""},
		{"already newest", "2.0.0", "major", ""},
		{"unparseable current", "not-a-version", "patch", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CeilingTarget(c.current, avail, c.maxUpdate, "gomod"); got != c.want {
				t.Errorf("CeilingTarget(%q, %q) = %q, want %q", c.current, c.maxUpdate, got, c.want)
			}
		})
	}
}
