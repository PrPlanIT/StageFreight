package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// OrderedClusters is the gitops: block — a clusterName→cluster map (the map key
// becomes GitOpsCluster.Name). Mirrors the repos:/forges:/registries: keyed maps
// so the cluster is named by its key, not a nested name: field.
type OrderedClusters []GitOpsCluster

func (o *OrderedClusters) UnmarshalYAML(n *yaml.Node) error {
	v, err := decodeIDMap(n, func(c *GitOpsCluster, name string) { c.Name = name })
	if err != nil {
		return fmt.Errorf("gitops: %w", err)
	}
	*o = v
	return nil
}

func (OrderedClusters) isIDMap() {}

// Primary returns the first declared cluster. The reconcile/scribe flows are
// single-cluster; the keyed map is for schema consistency + naming, not fan-out.
func (o OrderedClusters) Primary() (GitOpsCluster, bool) {
	if len(o) == 0 {
		return GitOpsCluster{}, false
	}
	return o[0], true
}

// ByName returns the declared cluster with the given name.
func (o OrderedClusters) ByName(name string) (GitOpsCluster, bool) {
	for _, c := range o {
		if c.Name == name {
			return c, true
		}
	}
	return GitOpsCluster{}, false
}

// GitOpsCluster is one gitops target cluster, keyed by its name in the gitops: map.
type GitOpsCluster struct {
	Name string `yaml:"-"` // from the map key

	Preset string `yaml:"preset,omitempty"`

	// Backend selects the GitOps reconciliation backend (e.g. "flux", "argo").
	Backend string `yaml:"backend"`

	// Endpoint is the cluster API server URL (e.g. https://host:6443).
	Endpoint string `yaml:"endpoint"`

	// Exposure classifies endpoint reachability (rule-based).
	Exposure ExposureRules `yaml:"exposure"`

	// Categories groups the cluster's namespaces into app inventory sections.
	Categories []InventoryCategory `yaml:"categories,omitempty"`

	// Inventory configures how the app inventory reads the cluster.
	Inventory InventoryConfig `yaml:"inventory,omitempty"`

	// OIDC defines authentication configuration for the cluster.
	OIDC OIDCConfig `yaml:"oidc"`
}

// Connection returns the connection descriptor the k8s/gitops packages consume.
func (c GitOpsCluster) Connection() ClusterConfig {
	return ClusterConfig{Name: c.Name, Endpoint: c.Endpoint, Exposure: c.Exposure, Categories: c.Categories, Groups: c.Inventory.Groups.From}
}

// InventoryConfig is gitops.<cluster>.inventory.
type InventoryConfig struct {
	Groups InventoryGroups `yaml:"groups,omitempty"`
}

// InventoryGroups names where an app's group is already declared in the cluster, so the
// inventory reuses it instead of a second declaration. An app with a group is an app,
// listed under that group; categories: covers apps without one.
type InventoryGroups struct {
	From GroupSource `yaml:"from,omitempty"`
}

// GroupSource reads a group from the Services, HTTPRoutes and Ingresses that expose an
// app: an annotation or a label, and when the value is a YAML mapping, one field of it.
type GroupSource struct {
	Annotation string `yaml:"annotation,omitempty"`
	Label      string `yaml:"label,omitempty"`
	Field      string `yaml:"field,omitempty"`
}

// Set reports whether a group source is configured.
func (g GroupSource) Set() bool { return g.Annotation != "" || g.Label != "" }

// InventoryCategory is one section of the app inventory. Sections render alphabetically.
type InventoryCategory struct {
	Name string `yaml:"name"`

	// Tier declares what the namespaces' workloads are: app, platform or hidden. Left
	// empty, discovery infers it per workload.
	Tier string `yaml:"tier,omitempty"`

	Namespaces []string `yaml:"namespaces"`
}

// ClusterConfig identifies a Kubernetes cluster and how to connect to it. Built
// from a GitOpsCluster via Connection(); it is an internal descriptor, not a YAML
// shape. CA trust is resolved from environment variables at runtime:
//   - <NAME>_CA_FILE: path to CA certificate file
//   - <NAME>_CA_B64: base64-encoded CA certificate PEM
//
// Name is uppercased with hyphens replaced by underscores for the env prefix.
type ClusterConfig struct {
	Name       string
	Endpoint   string
	Exposure   ExposureRules
	Categories []InventoryCategory
	Groups     GroupSource
}

// ExposureRules defines rule-based endpoint exposure classification.
// Rules are evaluated by precedence: endpoint > gateway > CIDR > service type.
// Conflicting equal-precedence rules → unknown. Never first-match-wins.
type ExposureRules struct {
	Rules []ExposureRule `yaml:"rules"`
}

// UnmarshalYAML accepts both the flat list form (exposure: [ -level: … ]) and the
// nested map form (exposure: {rules: [ … ]}). Rules is the only field, so the flat
// list is the canonical shape; the map form stays accepted for compatibility.
func (e *ExposureRules) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.SequenceNode:
		return n.Decode(&e.Rules)
	case yaml.MappingNode:
		var raw struct {
			Rules []ExposureRule `yaml:"rules"`
		}
		if err := n.Decode(&raw); err != nil {
			return err
		}
		e.Rules = raw.Rules
		return nil
	default:
		return fmt.Errorf("exposure must be a rules list or a {rules: …} map")
	}
}

// ExposureRule classifies endpoints into exposure levels.
// Match fields are AND within a rule (CIDR AND port if both specified).
type ExposureRule struct {
	Level          string   `yaml:"level"`     // internet | intranet | cluster
	Endpoints      []string `yaml:"endpoints"` // ip:port (highest precedence)
	Gateways       []string `yaml:"gateways"`
	IngressClasses []string `yaml:"ingress_classes"` // Ingress spec.ingressClassName
	CIDRs          []string `yaml:"cidrs"`
	Ports          []int    `yaml:"ports"`         // AND with CIDRs (empty = any port)
	ServiceTypes   []string `yaml:"service_types"` // ClusterIP | NodePort | LoadBalancer
}

// OIDCConfig defines OIDC authentication for cluster access.
// Token is resolved from environment variable STAGEFREIGHT_OIDC at runtime.
type OIDCConfig struct {
	Audience string `yaml:"audience"`
}
