package discovery

import (
	"time"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

// setReleased records when version v of dep was published. Versions older than Current
// are not update candidates and are not kept.
func setReleased(dep *supplychain.Dependency, v string, t time.Time) {
	if t.IsZero() || dep.Current != "" && compare(v, dep.Current, dep.Ecosystem) < 0 {
		return
	}
	if dep.Released == nil {
		dep.Released = map[string]time.Time{}
	}
	dep.Released[v] = t
}
