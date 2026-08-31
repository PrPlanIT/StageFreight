package config

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/presetref"
)

// A bound nothing sets is a claim the code does not honour. This asserts the resolver
// the loader actually builds carries one, so an unreachable source cannot leave a
// satellite frozen on old content behind a warning.
func TestLoaderResolverIsBounded(t *testing.T) {
	l := sourceAwareLoader{cacheDir: t.TempDir()}
	r := l.resolver()
	if r.MaxFallbackAge == 0 {
		t.Fatal("the loader's resolver has no fallback bound — an unreachable source would stand in forever")
	}
	if r.MaxFallbackAge != presetref.DefaultMaxFallbackAge {
		t.Errorf("MaxFallbackAge = %v, want the engine default %v", r.MaxFallbackAge, presetref.DefaultMaxFallbackAge)
	}
}
