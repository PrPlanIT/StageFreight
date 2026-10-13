package k8s

import (
	"context"
	"sync"

	"github.com/PrPlanIT/StageFreight/src/config"
)

type sweepResult struct {
	result *DiscoveryResult
	err    error
}

var (
	sweepMu sync.Mutex
	sweeps  = map[string]sweepResult{}
)

// Sweep discovers a cluster at most once per process for a repo and catalog. The
// inventory region and the {inventory.<cluster>.count} badge both read the cluster;
// sharing one sweep makes them one reading whichever renders first, and runs the
// lifecycle reconcile once.
func Sweep(ctx context.Context, cluster config.ClusterConfig, catalogPath, repoRoot string) (*DiscoveryResult, error) {
	key := repoRoot + "\x00" + catalogPath + "\x00" + cluster.Name
	sweepMu.Lock()
	defer sweepMu.Unlock()
	if s, ok := sweeps[key]; ok {
		return s.result, s.err
	}
	var s sweepResult
	client, err := NewClient(cluster)
	if err != nil {
		s.err = err
	} else {
		s.result, s.err = Discover(ctx, client, catalogPath, repoRoot, cluster)
		client.Close()
	}
	sweeps[key] = s
	return s.result, s.err
}

// Listed is the number of running workloads the inventory page lists.
func (r *DiscoveryResult) Listed() int {
	return len(r.Apps) + len(r.Platform) + len(r.Unresolved)
}
