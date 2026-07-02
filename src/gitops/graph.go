// Package gitops provides Flux CD graph discovery, change impact analysis,
// and reconciliation coordination.
//
// Core rule: if Flux already knows it, StageFreight discovers it — never asks for it.
// No duplicated topology config. No declared kustomization lists.
// Flux is truth. StageFreight is the intelligence + evidence layer.
package gitops

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// KustomizationKey uniquely identifies a Flux Kustomization.
// Identity is (namespace, name) — never bare name alone.
type KustomizationKey struct {
	Namespace string
	Name      string
}

func (k KustomizationKey) String() string {
	return k.Namespace + "/" + k.Name
}

// KustomizationNode is a discovered Flux Kustomization with its dependencies.
type KustomizationNode struct {
	Key       KustomizationKey
	Path      string // normalized, repo-root relative
	DependsOn []KustomizationKey
	SourceRef string
}

// FluxGraph is the discovered dependency graph of Flux Kustomizations.
type FluxGraph struct {
	Kustomizations map[KustomizationKey]KustomizationNode
	ReverseDeps    map[KustomizationKey][]KustomizationKey
}

// BootstrapState indicates whether Flux bootstrapping is needed.
type BootstrapState struct {
	Required bool
	Reason   string
}

// rawKustomization is a minimal YAML struct for parsing Flux Kustomization objects.
type rawKustomization struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Spec struct {
		Path      string `yaml:"path"`
		SourceRef struct {
			Name string `yaml:"name"`
		} `yaml:"sourceRef"`
		DependsOn []struct {
			Name      string `yaml:"name"`
			Namespace string `yaml:"namespace"`
		} `yaml:"dependsOn"`
	} `yaml:"spec"`
}

// NormalizePath cleans a path for consistent matching.
func NormalizePath(p string) string {
	p = filepath.ToSlash(filepath.Clean(p))
	p = strings.TrimPrefix(p, "./")
	return p
}

// DiscoverFluxGraph walks the repo and discovers all Flux Kustomization objects.
// Builds forward and reverse dependency graphs. No config needed — everything
// is derived from the actual manifests.
func DiscoverFluxGraph(root string) (*FluxGraph, error) {
	graph := &FluxGraph{
		Kustomizations: map[KustomizationKey]KustomizationNode{},
		ReverseDeps:    map[KustomizationKey][]KustomizationKey{},
	}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			return nil
		}

		b, err := os.ReadFile(path)
		if err != nil {
			return nil // skip unreadable files
		}

		// Multi-document YAML support — Flux repos frequently use --- separators.
		decoder := yaml.NewDecoder(bytes.NewReader(b))
		for {
			var k rawKustomization
			err := decoder.Decode(&k)
			if err == io.EOF {
				break
			}
			if err != nil {
				break // malformed doc, skip rest of file
			}

			if k.Kind != "Kustomization" {
				continue
			}
			// Skip kustomize.config.k8s.io Kustomizations (not Flux)
			if strings.HasPrefix(k.APIVersion, "kustomize.config.k8s.io") {
				continue
			}

			ns := k.Metadata.Namespace
			if ns == "" {
				ns = "flux-system"
			}

			key := KustomizationKey{
				Namespace: ns,
				Name:      k.Metadata.Name,
			}

			node := KustomizationNode{
				Key:       key,
				Path:      NormalizePath(k.Spec.Path),
				DependsOn: []KustomizationKey{},
				SourceRef: k.Spec.SourceRef.Name,
			}

			for _, d := range k.Spec.DependsOn {
				depNS := d.Namespace
				if depNS == "" {
					depNS = ns // same namespace as parent
				}
				node.DependsOn = append(node.DependsOn, KustomizationKey{
					Namespace: depNS,
					Name:      d.Name,
				})
			}

			// A pathless PATCH FRAGMENT (e.g. a sops decryption patch that shares the
			// bootstrap Kustomization's key, applied via the overlay's `patches:`) must not
			// overwrite the REAL root's path in this by-key map. NormalizePath("") is ".",
			// so the pathless node carries Path="." — test the RAW spec.path for emptiness.
			// The real path-bearing node always wins regardless of file-walk order;
			// otherwise the flux-system node collapses to the repo root and validation flags
			// non-manifest YAML (.gitlab-ci.yml, .sops.yaml, …) as "missing 'kind' key".
			// Pathless-only keys are still recorded (unchanged), so the graph stays complete.
			newPathless := strings.TrimSpace(k.Spec.Path) == ""
			if existing, ok := graph.Kustomizations[key]; ok && existing.Path != "" && existing.Path != "." && newPathless {
				continue
			}
			graph.Kustomizations[key] = node
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("walking repo: %w", err)
	}

	// Build reverse dependency map
	for _, k := range graph.Kustomizations {
		for _, dep := range k.DependsOn {
			graph.ReverseDeps[dep] = append(graph.ReverseDeps[dep], k.Key)
		}
	}

	return graph, nil
}

// DetectBootstrapRequired checks if Flux bootstrapping is needed.
// Stub for now — returns false. Hook wired for future extension.
func DetectBootstrapRequired(graph *FluxGraph) BootstrapState {
	return BootstrapState{Required: false}
}

// DuplicatePaths returns paths owned by multiple kustomizations.
func DuplicatePaths(graph *FluxGraph) map[string][]KustomizationKey {
	pathOwners := map[string][]KustomizationKey{}
	for _, k := range graph.Kustomizations {
		if k.Path != "" {
			pathOwners[k.Path] = append(pathOwners[k.Path], k.Key)
		}
	}
	dupes := map[string][]KustomizationKey{}
	for p, owners := range pathOwners {
		if len(owners) > 1 {
			dupes[p] = owners
		}
	}
	return dupes
}

// BuildRoots returns the unique, normalized, non-empty kustomization paths in
// the graph — the set of directories to `kustomize build` exactly once. Multiple
// Flux Kustomizations pointing at the same path collapse to a single root, so
// shared manifests are not rendered (and re-validated) once per owner.
// Deterministic: lexically sorted.
func BuildRoots(graph *FluxGraph) []string {
	seen := map[string]bool{}
	var roots []string
	for _, node := range graph.Kustomizations {
		if node.Path == "" || seen[node.Path] {
			continue
		}
		seen[node.Path] = true
		roots = append(roots, node.Path)
	}
	sort.Strings(roots)
	return roots
}

// ReconcileOrder returns the kustomizations in dependency order: a
// kustomization's dependencies always appear before it. This is what Flux's own
// controller honors via spec.dependsOn — reconciling in this order accelerates
// convergence and makes the reconcile output read top-down instead of randomly.
//
// Order is deterministic: ties at the same dependency depth are broken lexically
// by namespace/name, so identical graphs always produce identical sequences
// (the property FluxBackend.Plan claimed but a map range could never deliver).
//
// DependsOn edges to kustomizations not present in the graph are ignored (Flux
// tolerates cross-source dependencies). Cycles are broken deterministically: any
// nodes that cannot be topologically placed are appended in lexical order so
// reconcile still covers the whole estate rather than silently dropping them.
func ReconcileOrder(graph *FluxGraph) []KustomizationKey {
	order, placed := kahn(graph)

	// Cycle remainder — append anything unplaced in lexical order so coverage
	// is never silently reduced by a dependency cycle.
	if len(order) < len(graph.Kustomizations) {
		var rest []KustomizationKey
		for key := range graph.Kustomizations {
			if !placed[key] {
				rest = append(rest, key)
			}
		}
		SortKeys(rest)
		order = append(order, rest...)
	}

	return order
}

// kahn runs Kahn's topological sort over the graph, deps-first with a lexical
// tiebreak at each depth (deterministic). It returns the placeable order and the
// set of nodes that were placed. Nodes absent from `placed` are unorderable — in,
// or downstream of, a dependency cycle. DependsOn edges to kustomizations not in
// the graph are ignored (Flux tolerates cross-source deps).
func kahn(graph *FluxGraph) (order []KustomizationKey, placed map[KustomizationKey]bool) {
	indeg := make(map[KustomizationKey]int, len(graph.Kustomizations))
	for key, node := range graph.Kustomizations {
		n := 0
		for _, d := range node.DependsOn {
			if _, ok := graph.Kustomizations[d]; ok {
				n++
			}
		}
		indeg[key] = n
	}

	var ready []KustomizationKey
	for key, n := range indeg {
		if n == 0 {
			ready = append(ready, key)
		}
	}
	SortKeys(ready)

	order = make([]KustomizationKey, 0, len(graph.Kustomizations))
	placed = make(map[KustomizationKey]bool, len(graph.Kustomizations))
	for len(ready) > 0 {
		k := ready[0]
		ready = ready[1:]
		order = append(order, k)
		placed[k] = true

		var newlyReady []KustomizationKey
		for _, dependent := range graph.ReverseDeps[k] {
			if placed[dependent] {
				continue
			}
			indeg[dependent]--
			if indeg[dependent] == 0 {
				newlyReady = append(newlyReady, dependent)
			}
		}
		if len(newlyReady) > 0 {
			ready = append(ready, newlyReady...)
			SortKeys(ready)
		}
	}
	return order, placed
}

// CycleNodes returns the set of kustomizations that cannot be topologically
// placed — those in, or downstream of, a dependency cycle. These are the keys a
// validation verdict marks as failed with a cycle reason: the dependency intent
// is incoherent and Flux itself would deadlock on them.
func CycleNodes(graph *FluxGraph) map[KustomizationKey]bool {
	_, placed := kahn(graph)
	cycle := map[KustomizationKey]bool{}
	for key := range graph.Kustomizations {
		if !placed[key] {
			cycle[key] = true
		}
	}
	return cycle
}

// DanglingDeps returns, per kustomization, the dependsOn references that point at
// a kustomization not present in the discovered graph. The referrer is the key a
// validation verdict marks — its declared dependency cannot be satisfied from
// this repository.
func DanglingDeps(graph *FluxGraph) map[KustomizationKey][]KustomizationKey {
	out := map[KustomizationKey][]KustomizationKey{}
	for key, node := range graph.Kustomizations {
		for _, d := range node.DependsOn {
			if _, ok := graph.Kustomizations[d]; !ok {
				out[key] = append(out[key], d)
			}
		}
	}
	return out
}

// Orphans returns kustomizations with no dependents and no dependencies.
func Orphans(graph *FluxGraph) []KustomizationKey {
	var orphans []KustomizationKey
	for key, node := range graph.Kustomizations {
		if len(node.DependsOn) == 0 && len(graph.ReverseDeps[key]) == 0 {
			orphans = append(orphans, key)
		}
	}
	SortKeys(orphans)
	return orphans
}

// SortKeys sorts kustomization keys lexically by namespace/name.
func SortKeys(keys []KustomizationKey) {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Namespace == keys[j].Namespace {
			return keys[i].Name < keys[j].Name
		}
		return keys[i].Namespace < keys[j].Namespace
	})
}
