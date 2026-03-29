// Package docker provides Docker lifecycle orchestration for StageFreight.
// The Compose backend uses docker compose as the execution engine.
// This is Docker lifecycle orchestration, not a docker-compose wrapper.
package docker

import (
	"fmt"
	"strconv"
	"time"
)

// TargetSelector declares which hosts are eligible for reconciliation.
// Group-based initially (existing Ansible groups). Extensible later
// only if groups become insufficient.
type TargetSelector struct {
	Groups []string `yaml:"groups"`
	// Future: Capabilities, Roles, Scope — deferred until needed
}

// HostTarget represents a resolved Docker reconciliation target.
type HostTarget struct {
	Name      string
	Address   string
	Groups    []string          // populated from inventory group membership
	Vars      map[string]string // host variables from inventory
	Transport HostTransport     // established during Prepare phase
}

// StackInfo describes a discovered IaC stack.
type StackInfo struct {
	Scope       string // host name or group name
	ScopeKind   string // "host" or "group"
	Name        string // stack directory name
	Path        string // relative path from repo root
	ComposeFile string // detected compose filename
	EnvFiles    []EnvFile
	Scripts     []string // pre.sh, deploy.sh, post.sh
	DeployKind  string   // "compose", "script", "unmanaged"
}

// EnvFile describes a discovered environment file within a stack.
type EnvFile struct {
	Path      string // relative to stack dir
	FullPath  string // absolute path
	Encrypted bool   // SOPS-encrypted (detected by naming or content)
}

// DriftResult describes the drift state of a single stack on a host.
type DriftResult struct {
	Host        string
	Stack       string
	Drifted     bool
	Tier        int    // 1 = bundle hash, 2 = container config hash
	Reason      string
	BundleHash  string // current computed hash
	StoredHash  string // last known hash
}

// DeployResult describes the outcome of deploying a single stack.
type DeployResult struct {
	Host     string
	Stack    string
	Success  bool
	Duration time.Duration
	Message  string
}

// DockerPlanMeta is the typed metadata for a Docker plan action.
// Internally, backends operate on this. Serialized to Metadata map for transport.
type DockerPlanMeta struct {
	Scope      string
	ScopeKind  string
	Stack      string
	Path       string
	BundleHash string
	StoredHash string
	DriftTier  int
	DeployKind string
}

// ToMetadata serializes to the generic transport map.
func (m DockerPlanMeta) ToMetadata() map[string]string {
	return map[string]string{
		"scope":       m.Scope,
		"scope_kind":  m.ScopeKind,
		"stack":       m.Stack,
		"path":        m.Path,
		"bundle_hash": m.BundleHash,
		"stored_hash": m.StoredHash,
		"drift_tier":  fmt.Sprintf("%d", m.DriftTier),
		"deploy_kind": m.DeployKind,
	}
}

// ParseDockerPlanMeta deserializes from the generic transport map.
func ParseDockerPlanMeta(m map[string]string) DockerPlanMeta {
	tier := 0
	if v, ok := m["drift_tier"]; ok {
		if parsed, err := strconv.Atoi(v); err == nil {
			tier = parsed
		}
	}
	return DockerPlanMeta{
		Scope:      m["scope"],
		ScopeKind:  m["scope_kind"],
		Stack:      m["stack"],
		Path:       m["path"],
		BundleHash: m["bundle_hash"],
		StoredHash: m["stored_hash"],
		DriftTier:  tier,
		DeployKind: m["deploy_kind"],
	}
}

// StackAction is a typed execution intent for the transport layer.
// Represents WHAT to execute, not HOW today's transport executes it.
// Transport receives this, compiles it to whatever form it needs.
// No absolute paths, no filesystem assumptions, no transport coupling.
type StackAction struct {
	Target      string // host identity
	Stack       string // scope/name
	Action      string // "up", "down", "restart"
	ProjectName string // docker compose -p flag
	WorkDir     string // working directory (relative to bundle or host-resolved)

	// Staged bundle: transport decides how to materialize this.
	// SSH copies it to remote tmpdir. Agent receives it as payload.
	BundleDir string // local staging root containing all needed files

	// Compose file and env files — relative to BundleDir.
	ComposeFile string   // e.g. "compose.yaml"
	EnvFiles    []string // e.g. [".env", "app_secret.env"]

	// Hooks as ordered execution steps, not raw paths.
	Hooks []Hook
}

// Hook is a lifecycle hook within a stack action.
type Hook struct {
	Phase string // "pre" | "post"
	Path  string // relative to BundleDir
}

// ExecResult is the structured outcome of a transport execution.
// All transports (local, SSH, future agents) return the same shape.
// Full stderr captured — renderer decides how to tail/truncate.
type ExecResult struct {
	Success  bool
	ExitCode int
	Stdout   string
	Stderr   string
	Duration time.Duration
}

// StackInspection is the canonical runtime state of a compose project.
// Reduced model — only the signals needed for drift detection.
type StackInspection struct {
	Project    string
	Services   []ServiceRuntimeState
}

// ServiceRuntimeState is the canonical runtime state of a single compose service.
// Primary Tier 2 signal: ConfigHash.
type ServiceRuntimeState struct {
	Service     string
	ConfigHash  string // com.docker.compose.config-hash label
	Image       string
	Running     bool
	State       string // running, exited, dead, etc.
	ContainerID string
}

// HashStamps tracks last-known hashes for drift detection.
// Stored in .stagefreight-state.yml (git-tracked).
type HashStamps struct {
	Stacks map[string]StackStamp `yaml:"stacks"` // key: "scope/stack"
}

// StackStamp records the hash state of a stack after successful deployment.
type StackStamp struct {
	BundleHash  string    `yaml:"bundle_hash"`
	DeployedAt  time.Time `yaml:"deployed_at"`
}
