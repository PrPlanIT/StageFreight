// Package k8s provides Kubernetes cluster discovery for the k8s-inventory
// narrator module. Discovers workloads, groups by app identity, augments
// with routes/services, and classifies into tiers.
//
// Three layers (never collapse):
//   - Discovery: authority (what the cluster has)
//   - Catalog: meaning (human descriptions, overrides, graveyard)
//   - Renderer: presentation (stable markdown output)
package k8s

import "time"

// AppKey uniquely identifies an application within the cluster.
// Scoped to namespace + resolved identity.
type AppKey struct {
	Namespace string
	Identity  string
}

// WorkloadIdentity records how an app's identity was resolved.
// Frozen at discovery time — never recomputed during augmentation.
type WorkloadIdentity struct {
	Key    AppKey
	Source string // "label/instance", "label/name", "helm", "ownerRef", "name"
	RootUID string // UID of the root owner (for collision detection)
}

// Tier classifies an app's visibility in the overview.
type Tier string

const (
	TierApp      Tier = "app"
	TierPlatform Tier = "platform"
	TierHidden   Tier = "hidden"
)

// AppRecord is the fully resolved, grouped representation of an application.
// One AppRecord per app identity per namespace — multiple workloads merge.
type AppRecord struct {
	Key           AppKey
	FriendlyName  string // from catalog, or derived from identity
	Category      string // from CategoryResolver
	Tier          Tier
	Description   string // from catalog
	Components    []ComponentRef
	WorkloadKinds []string // deduped: ["Deployment"], ["StatefulSet", "Deployment"] → ["Mixed"]
	Images        []ImageRef
	Version       string // resolved via strict precedence
	Hosts    []string       // deduplicated, sorted hostnames from routes
	Exposure ExposureResult // classified from exposure rules
	Replicas      string // "ready/desired" format
	Status        Status
	Collision     bool   // true if identity was disambiguated via #shortUID
	Sources       []DeclaredSource // authoritative repo paths from Flux graph
	HomepageURL   string
	DocsURL       string
	SourceURL     string
}

// DeclaredSource links an app to its authoritative declaration in the repo.
// Resolved from Flux objects, not guessed from filenames.
// Sources are authoritative or empty — never partial guesses.
type DeclaredSource struct {
	Kind     string // overlay, helmrelease, kustomization, policy
	RepoPath string // repo-relative authoritative path
	Relation string // deploys, configures, secures, depends_on
	Primary  bool
}

// Source relation constants — prevent typos across discovery/renderer.
const (
	SourceRelationDeploys    = "deploys"
	SourceRelationConfigures = "configures"
	SourceRelationSecures    = "secures"
	SourceRelationDependsOn  = "depends_on"
)

// ExposureLevel classifies how an app is exposed.
type ExposureLevel string

const (
	ExposureInternet ExposureLevel = "internet"
	ExposureIntranet ExposureLevel = "intranet"
	ExposureCluster  ExposureLevel = "cluster"
	ExposureUnknown  ExposureLevel = "unknown"
)

// ExposureResult is the classified exposure of an app.
// Declared is from rule matching. Observed is future (external probe).
// Confidence is derived from match type, never user-set.
type ExposureResult struct {
	Declared   ExposureLevel `json:"declared"`
	Observed   ExposureLevel `json:"observed,omitempty"` // future: from external probe
	Gateway    string        `json:"gateway,omitempty"`
	Endpoint   string        `json:"endpoint,omitempty"`
	Confidence string        `json:"confidence"` // exact | inferred | fallback | unknown
	RuleIndex  int           `json:"rule_index"` // which rule matched (-1 if none)
}

// ComponentRef identifies a single workload component within a multi-component app.
type ComponentRef struct {
	Name string        // e.g. "core", "registry", "jobservice"
	Kind string        // e.g. "Deployment", "StatefulSet"
	Role ComponentRole // classified role (database, cache, primary, etc.)
}

// ImageRef holds a parsed container image reference.
type ImageRef struct {
	Repository string // e.g. "docker.io/library/redis"
	Tag        string // e.g. "7.4.2", "latest"
	Digest     string // e.g. "sha256:abc..." (if available from pod spec)
}

// ExposureRef represents a discovered route/ingress attachment.
// Interface-based: HTTPRoute today, Ingress extensible later.
type ExposureRef struct {
	Kind    string // "HTTPRoute", "Ingress"
	Host    string
	Name    string
	Gateway string // parentRef gateway name for exposure classification
}

// Status represents the health state of an application.
type Status string

const (
	StatusHealthy  Status = "Healthy"
	StatusDegraded Status = "Degraded"
	StatusDown     Status = "Down"
	StatusUnknown  Status = "Unknown"
)

// ComputeStatus derives health from ready vs desired replica counts.
func ComputeStatus(ready, desired int32) Status {
	if desired <= 0 {
		return StatusUnknown
	}
	if ready == desired {
		return StatusHealthy
	}
	if ready > 0 {
		return StatusDegraded
	}
	return StatusDown
}

// GraveyardEntry represents a retired application (catalog-only, never inferred).
type GraveyardEntry struct {
	Name      string `yaml:"name"`
	Namespace string `yaml:"namespace"`
	Category  string `yaml:"category"`
	Reason    string `yaml:"reason"`
	DocsURL   string `yaml:"docs_url"`
}

// DiscoveryResult holds the complete output of cluster discovery.
type DiscoveryResult struct {
	Apps       []AppRecord
	Platform   []AppRecord
	Graveyard  []GraveyardEntry
	ObservedAt time.Time
	Cluster    string
}

// SidecarImages is the set of known sidecar/infrastructure container image
// substrings that should be excluded from version detection.
var SidecarImages = []string{
	"istio-proxy",
	"linkerd-proxy",
	"cilium",
	"envoy",
	"kube-rbac-proxy",
	"vault-agent",
}
