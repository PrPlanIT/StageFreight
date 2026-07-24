package gitver

import (
	"regexp"
	"testing"
)

// TestCheckTagSourceExclusivity: a tag matching two tag_sources is a config error
// (mutual exclusivity), while exclusive patterns pass.
func TestCheckTagSourceExclusivity(t *testing.T) {
	exclusive := map[string]*regexp.Regexp{
		"stable": regexp.MustCompile(`^v\d+\.\d+\.\d+$`),
		"pre":    regexp.MustCompile(`^v\d+\.\d+\.\d+-.+$`),
	}
	if err := checkTagSourceExclusivity([]string{"v1.2.3", "v1.2.3-rc1"}, exclusive); err != nil {
		t.Fatalf("mutually-exclusive patterns should pass: %v", err)
	}

	overlapping := map[string]*regexp.Regexp{
		"a": regexp.MustCompile(`^v\d`),
		"b": regexp.MustCompile(`\.\d+$`),
	}
	if err := checkTagSourceExclusivity([]string{"v1.2.3"}, overlapping); err == nil {
		t.Fatal("a tag matching two tag_sources must error (mutual exclusivity)")
	}
}
