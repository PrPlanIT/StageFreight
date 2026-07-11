package version

import "testing"

func TestValidateConstraint(t *testing.T) {
	ok := []string{"1.26.4", "1.26.x", "1.x.x", "x.x.x", "0.9.138", "v1.26.4"}
	for _, c := range ok {
		if err := ValidateConstraint(c); err != nil {
			t.Errorf("ValidateConstraint(%q) = %v, want nil", c, err)
		}
	}
	bad := []string{"1.26", "1.x.4", "1..4", "1.y.z", ""}
	for _, c := range bad {
		if err := ValidateConstraint(c); err == nil {
			t.Errorf("ValidateConstraint(%q) = nil, want error", c)
		}
	}
}

func TestConstraintMatches(t *testing.T) {
	cases := []struct {
		c, v string
		want bool
	}{
		{"1.26.x", "1.26.7", true},
		{"1.26.x", "1.27.0", false},
		{"1.x.x", "1.99.3", true},
		{"1.x.x", "2.0.0", false},
		{"x.x.x", "9.9.9", true},
		{"1.26.4", "1.26.4", true},
		{"1.26.4", "1.26.5", false},
	}
	for _, tc := range cases {
		if got := ConstraintMatches(tc.c, tc.v); got != tc.want {
			t.Errorf("ConstraintMatches(%q, %q) = %v, want %v", tc.c, tc.v, got, tc.want)
		}
	}
}

func TestSelectConstraint(t *testing.T) {
	avail := []string{"1.26.3", "1.26.7", "1.27.0", "1.27.1-rc1", "2.0.0"}
	cases := []struct {
		c, want string
	}{
		{"1.26.x", "1.26.7"},      // highest patch of the line
		{"1.27.x", "1.27.0"},      // rc excluded (prerelease)
		{"1.x.x", "1.27.0"},       // highest minor within major 1, stable
		{"x.x.x", "2.0.0"},        // highest overall stable
		{"1.26.7", "1.26.7"},      // exact present
		{"9.9.x", ""},             // line doesn't exist
	}
	for _, tc := range cases {
		if got := SelectConstraint(tc.c, avail); got != tc.want {
			t.Errorf("SelectConstraint(%q) = %q, want %q", tc.c, got, tc.want)
		}
	}
}
