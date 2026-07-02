package gitops

import (
	"os"
	"path/filepath"
	"testing"
)

// A sops-decryption patch is a Kustomization doc with the SAME key as the bootstrap
// root but no spec.path. It must not clobber the real root's path in the by-key graph
// map — otherwise the node collapses to the repo root and validation flags non-manifest
// YAML (.gitlab-ci.yml, .sops.yaml, …) as "missing 'kind' key". Regression for that bug.
func TestPathlessPatchDoesNotClobberRoot(t *testing.T) {
	dir := t.TempDir()
	// Real bootstrap Kustomization (has spec.path).
	write(t, dir, "gotk-sync.yaml", `apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: flux-system
  namespace: flux-system
spec:
  path: ./fluxcd/clusters/overlays/production
`)
	// Pathless decryption patch, same key — sorts AFTER gotk-sync in the file walk.
	write(t, dir, "sops-patch.yaml", `apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: flux-system
  namespace: flux-system
spec:
  decryption:
    provider: sops
`)

	g, err := DiscoverFluxGraph(dir)
	if err != nil {
		t.Fatalf("DiscoverFluxGraph: %v", err)
	}
	got := g.Kustomizations[KustomizationKey{Namespace: "flux-system", Name: "flux-system"}].Path
	if got != "fluxcd/clusters/overlays/production" {
		t.Errorf("flux-system path = %q; want fluxcd/clusters/overlays/production (pathless patch clobbered the real root → repo-root validation)", got)
	}
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
