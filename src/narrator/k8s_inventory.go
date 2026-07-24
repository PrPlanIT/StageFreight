package narrator

import (
	"context"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/diag"
	"github.com/PrPlanIT/StageFreight/src/k8s"
)

// K8sInventoryModule renders a cluster app inventory via live Kubernetes discovery.
// Orchestration only — all k8s auth, discovery, and rendering logic lives in src/k8s/.
type K8sInventoryModule struct {
	CatalogPath   string               // optional path to catalog
	CommitSHA     string               // optional git SHA for provenance
	RepoRoot      string               // for source link verification and Flux graph resolution
	ClusterConfig config.ClusterConfig // passed to k8s.NewClient for auth + to Discover for exposure rules
}

// Render discovers workloads from the live cluster, groups by app identity,
// classifies, and produces stable markdown. Returns empty string on error
// (Module interface contract — errors are logged via diag).
func (m *K8sInventoryModule) Render() string {
	client, err := k8s.NewClient(m.ClusterConfig)
	if err != nil {
		diag.Error("k8s-inventory: %s", err)
		return ""
	}
	defer client.Close()

	result, err := k8s.Discover(context.Background(), client, m.CatalogPath, m.RepoRoot, m.ClusterConfig.Exposure)
	if err != nil {
		diag.Error("k8s-inventory: %s", err)
		return ""
	}

	return k8s.RenderOverview(result, m.CommitSHA)
}
