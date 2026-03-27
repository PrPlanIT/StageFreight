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
