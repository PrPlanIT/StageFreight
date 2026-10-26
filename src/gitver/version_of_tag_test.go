package gitver

import "testing"

func TestVersionOfTag(t *testing.T) {
	cases := []struct {
		tag, version, base string
		pre                bool
	}{
		{"v1.2.3", "1.2.3", "1.2.3", false},
		{"v1.2.3-rc.1", "1.2.3-rc.1", "1.2.3", true},
		{"beta_v0.614", "beta_v0.614", "beta_v0.614", false},
	}
	for _, tc := range cases {
		v := VersionOfTag(tc.tag, "abc")
		if v.Version != tc.version || v.Base != tc.base || v.IsPrerelease != tc.pre || v.SHA != "abc" || !v.IsRelease {
			t.Errorf("%s → %+v", tc.tag, v)
		}
	}
}
