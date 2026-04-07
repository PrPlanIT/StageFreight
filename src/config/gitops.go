package config

// GitOpsConfig defines configuration for the gitops lifecycle mode.
type GitOpsConfig struct {
	Preset string `yaml:"preset,omitempty"`

	// Backend selects the GitOps reconciliation backend (e.g. "flux", "argo").
	Backend string `yaml:"backend"`

	// Cluster defines the target Kubernetes cluster.
	Cluster ClusterConfig `yaml:"cluster"`

	// OIDC defines authentication configuration for the cluster.
	OIDC OIDCConfig `yaml:"oidc"`
}

// ClusterConfig identifies a Kubernetes cluster and how to connect to it.
// CA trust is resolved from environment variables at runtime:
//   - <NAME>_CA_FILE: path to CA certificate file
//   - <NAME>_CA_B64: base64-encoded CA certificate PEM
//
// Name is uppercased with hyphens replaced by underscores for the env prefix.
type ClusterConfig struct {
	Name     string        `yaml:"name"`
	Server   string        `yaml:"server"`
	Exposure ExposureRules `yaml:"exposure"`
}

// ExposureRules defines rule-based endpoint exposure classification.
// Rules are evaluated by precedence: endpoint > gateway > CIDR > service type.
// Conflicting equal-precedence rules → unknown. Never first-match-wins.
type ExposureRules struct {
	Rules []ExposureRule `yaml:"rules"`
}

// ExposureRule classifies endpoints into exposure levels.
// Match fields are AND within a rule (CIDR AND port if both specified).
type ExposureRule struct {
	Level        string   `yaml:"level"`         // internet | intranet | cluster
	Endpoints    []string `yaml:"endpoints"`     // ip:port (highest precedence)
	Gateways     []string `yaml:"gateways"`
	CIDRs        []string `yaml:"cidrs"`
	Ports        []int    `yaml:"ports"`         // AND with CIDRs (empty = any port)
	ServiceTypes []string `yaml:"service_types"` // ClusterIP | NodePort | LoadBalancer
}

// OIDCConfig defines OIDC authentication for cluster access.
// Token is resolved from environment variable STAGEFREIGHT_OIDC at runtime.
type OIDCConfig struct {
	Audience string `yaml:"audience"`
}

// DefaultGitOpsConfig returns sensible defaults for gitops configuration.
func DefaultGitOpsConfig() GitOpsConfig {
	return GitOpsConfig{
		Backend: "flux",
	}
}
