package cistate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/PrPlanIT/StageFreight/src/atomicfile"
	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/paths"
	"github.com/PrPlanIT/StageFreight/src/runner"
)

// StatePath is the workspace-relative path where pipeline state is persisted.
const StatePath = paths.Root + "/pipeline.json"

// SubsystemsDir holds one fragment per subsystem outcome (subsystems/build.json,
// subsystems/security.json, …). These fragments — NOT pipeline.json — are the
// single carrier of subsystem state ACROSS jobs.
//
// Why: CI jobs are isolated workspaces whose outputs are recombined by an artifact
// system with no merge — only file union, where same-path files clobber. A single
// shared pipeline.json therefore cannot accumulate across jobs: when a downstream
// job (publish) downloads two upstreams that each wrote pipeline.json, the
// last-written copy overwrites the other, silently dropping the subsystems only one
// job recorded. That order-dependent clobber is what made publish's authorization
// gate see "security did not run". Per-name fragments never collide across jobs
// (perform→build.json, review→security.json), so ReadState UNIONs them regardless
// of download order. pipeline.json remains each job's LOCAL merged view (the read
// model); it is not forwarded as a contested cross-job ledger.
const SubsystemsDir = paths.Root + "/subsystems"

// State is the per-run ledger for the current pipeline workspace.
// Each subsystem records what it did; downstream stages read the ledger
// instead of probing files.
type State struct {
	Version    int                    `json:"version"`
	CI         CIState                `json:"ci"`
	Runner     runner.ExecutionReport `json:"runner,omitempty"`
	Build      BuildState             `json:"build"`
	Security   SecurityState          `json:"security"`
	Release    ReleaseState           `json:"release"`
	Subsystems []SubsystemState       `json:"subsystems,omitempty"`
	Retention  RetentionState         `json:"retention,omitempty"`
}

// RetentionState records cache retention enforcement results.
// Authoritative — governance and diagnostics can inspect this.
type RetentionState struct {
	Local    *LocalRetentionRecord    `json:"local,omitempty"`
	External *ExternalRetentionRecord `json:"external,omitempty"`
}

// LocalRetentionRecord records local cache retention results.
type LocalRetentionRecord struct {
	Dir           string `json:"dir"`
	EntriesBefore int    `json:"entries_before"`
	Pruned        int    `json:"pruned"`
	PrunedBytes   int64  `json:"pruned_bytes"`
}

// ExternalRetentionRecord records external cache retention results.
type ExternalRetentionRecord struct {
	Registry string   `json:"registry"`
	Prefix   string   `json:"prefix"`
	Total    int      `json:"total"`
	Pruned   int      `json:"pruned"`
	Kept     int      `json:"kept"`
	Errors   []string `json:"errors,omitempty"`
}

// SubsystemState is the generic lifecycle phase record.
// All subsystems register here regardless of mode. The resolver
// uses this list — never hardcoded field names.
type SubsystemState struct {
	Name         string `json:"name"`
	Attempted    bool   `json:"attempted"`
	Completed    bool   `json:"completed"`
	Skipped      bool   `json:"skipped"`
	AllowFailure bool   `json:"allow_failure"` // true = non-vital; failure produces warning, not fail
	Required     bool   `json:"required"`      // true = failure is a hard pipeline fail
	Outcome      string `json:"outcome"`       // success | failed | skipped | warning | not_applicable | cancelled
	Reason       string `json:"reason,omitempty"`
	// Blocking is the CONTROL truth about this subsystem's subject: may that subject
	// continue producing distributable artifacts? A phase that consumes this NEVER
	// branches on anything else. It is deliberately separate from Outcome (what happened)
	// and Required (a forge-status policy). A remediated source is Blocking (the fix lives
	// in Replacement, not in this subject), so it must never build.
	Blocking bool `json:"blocking,omitempty"`
	// Replacement is LINEAGE, never control: the commit that supersedes this subject when a
	// fix was produced (the remediation candidate). Consumed by narrate / publish / the forge
	// status renderer / webhooks — never by the orchestrator's build decision.
	Replacement string `json:"replacement,omitempty"`
	// Results is the subsystem's recorded FACTS — flat key→value pairs a subsystem
	// computed during the run (test: passed/failed/total/coverage; security:
	// blocking/low/…; publish: tags/registries). Consumed by the stencil fact
	// resolver as {<domain>.<key>} tokens. Riding the subsystem shard keeps facts
	// union-safe across forwarded job artifacts (same guarantee as Outcome). Generic
	// by design: any subsystem (any modality) records facts without a schema change.
	Results map[string]string `json:"results,omitempty"`
}

// PipelineStatus derives the aggregate pipeline outcome from all subsystems.
// States: passing, warning, failing, unknown.
//
// Resolution rules (platform-agnostic, policy-aware):
//   - Any required subsystem with outcome "failed" → failing
//   - Any non-required subsystem with outcome "failed" + allow_failure → warning
//   - Any subsystem with outcome "warning" → warning
//   - Nothing attempted → unknown
//   - Otherwise → passing
func (st *State) PipelineStatus() string {
	subs := st.Subsystems

	anyAttempted := false
	hasWarning := false

	for _, s := range subs {
		if !s.Attempted {
			continue
		}
		anyAttempted = true

		switch s.Outcome {
		case "failed":
			if s.AllowFailure {
				hasWarning = true
			} else {
				return "failing"
			}
		case "warning":
			hasWarning = true
		case "skipped":
			// Intentional skip is neutral — not a warning unless the subsystem was required.
			if s.Required {
				hasWarning = true
			}
		case "not_applicable":
			// Subsystem doesn't apply to this lifecycle mode. Always neutral.
		case "cancelled":
			// Cancelled subsystem: required → failing, otherwise → warning.
			if !s.AllowFailure {
				return "failing"
			}
			hasWarning = true
		}
	}

	if !anyAttempted {
		return "unknown"
	}
	if hasWarning {
		return "warning"
	}
	return "passing"
}

// CIState captures the CI environment for this pipeline run, plus the run's
// identity facts ({project}/{modality}/{pipeline_url}/{commit_title}) recorded by
// the phases that can compute them (best-effort fill; empty stays empty).
type CIState struct {
	Provider   string `json:"provider"`
	PipelineID string `json:"pipeline_id"`
	Ref        string `json:"ref,omitempty"`
	Branch     string `json:"branch,omitempty"`
	Tag        string `json:"tag,omitempty"`
	SHA        string `json:"sha"`

	Project     string `json:"project,omitempty"`      // repo/project name ({project})
	Modality    string `json:"modality,omitempty"`     // lifecycle mode ({modality})
	PipelineURL string `json:"pipeline_url,omitempty"` // forge pipeline URL ({pipeline_url})
	CommitTitle string `json:"commit_title,omitempty"` // HEAD commit subject ({commit_title})
	Version     string `json:"version,omitempty"`      // detected version string ({version})

	// StartedAt is stamped (unix seconds) on the run's FIRST state write — the
	// audition, in practice — and rides the state artifact across jobs. narrate
	// derives DurationSecs from it at gather time; {duration} renders from the
	// recorded value, so the render itself stays deterministic.
	StartedAt    int64 `json:"started_at,omitempty"`
	DurationSecs int   `json:"duration_secs,omitempty"`
}

// BuildState holds build-specific domain metadata.
// Lifecycle tracking (attempted/completed/outcome) is in Subsystems.
type BuildState struct {
	ProducedImages bool   `json:"produced_images"`
	PublishedCount int    `json:"published_count"`
	ManifestPath   string `json:"manifest_path,omitempty"`
}

// SecurityState holds security-specific domain metadata.
// Lifecycle tracking is in Subsystems.
type SecurityState struct{}

// ReleaseState holds release-specific domain metadata.
// Lifecycle tracking is in Subsystems.
type ReleaseState struct {
	Eligible bool `json:"eligible"`
}

// GetSubsystem returns the subsystem entry by name, or nil if not found.
func (st *State) GetSubsystem(name string) *SubsystemState {
	for i := range st.Subsystems {
		if st.Subsystems[i].Name == name {
			return &st.Subsystems[i]
		}
	}
	return nil
}

// ReadState reads pipeline state from the workspace. Returns a zero State
// (Version: 1) on missing file — missing state is normal when the first
// subsystem hasn't run yet. Only errors on I/O or parse failures for an
// existing file.
func ReadState(rootDir string) (*State, error) {
	p := filepath.Join(rootDir, StatePath)
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &State{Version: 1}, nil
		}
		return nil, fmt.Errorf("reading pipeline state: %w", err)
	}

	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parsing pipeline state: %w", err)
	}
	overlaySubsystemShards(rootDir, &st)
	return &st, nil
}

// overlaySubsystemShards folds every subsystems/<name>.json shard into st,
// upserting by name. This is what turns cross-job artifact forwarding from a
// clobber into a union: even when this workspace's pipeline.json is the subset
// copy that won an artifact-download race, the shards restore each subsystem the
// other jobs recorded. Best-effort — a missing dir or an unreadable/!parse shard
// is skipped, never fatal, so a corrupt shard can only lose its own subsystem
// (which then reads as "did not run" — fail closed), never break state reads.
func overlaySubsystemShards(rootDir string, st *State) {
	dir := filepath.Join(rootDir, SubsystemsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var s SubsystemState
		if err := json.Unmarshal(data, &s); err != nil || s.Name == "" {
			continue
		}
		st.RecordSubsystem(s)
	}
}

// writeSubsystemShards persists each subsystem as its own file so cross-job
// artifact forwarding unions rather than clobbers (see SubsystemsDir).
func writeSubsystemShards(rootDir string, subs []SubsystemState) error {
	if len(subs) == 0 {
		return nil
	}
	dir := filepath.Join(rootDir, SubsystemsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating subsystems dir: %w", err)
	}
	for _, s := range subs {
		data, err := json.MarshalIndent(s, "", "  ")
		if err != nil {
			return fmt.Errorf("marshaling subsystem %q: %w", s.Name, err)
		}
		data = append(data, '\n')
		if err := atomicfile.WriteFile(filepath.Join(dir, shardFileName(s.Name)), data, 0o644); err != nil {
			return fmt.Errorf("writing subsystem shard %q: %w", s.Name, err)
		}
	}
	return nil
}

// shardFileName maps a subsystem name to a safe shard filename. Subsystem names
// are simple identifiers ("build", "security", ...); any path-unsafe rune is
// replaced so a name can never escape SubsystemsDir.
func shardFileName(name string) string {
	safe := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			safe = append(safe, r)
		default:
			safe = append(safe, '_')
		}
	}
	return string(safe) + ".json"
}

// WriteState writes pipeline state atomically (tmp + fsync + rename).
// Normalizes Version to 1 on write and stamps StartedAt on the run's first write
// (the {duration} fact's zero point — recording time is impure by nature; the
// RENDER of the recorded value stays deterministic).
func WriteState(rootDir string, st *State) error {
	st.Version = 1
	if st.CI.StartedAt == 0 {
		st.CI.StartedAt = time.Now().Unix()
	}

	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling pipeline state: %w", err)
	}
	data = append(data, '\n')

	if err := atomicfile.WriteFile(filepath.Join(rootDir, StatePath), data, 0o644); err != nil {
		return err
	}
	// Mirror each subsystem to its own shard so downstream jobs union rather than
	// clobber subsystem outcomes across forwarded artifacts (see SubsystemsDir).
	return writeSubsystemShards(rootDir, st.Subsystems)
}

// RecordSubsystem upserts a subsystem entry by name. A record carrying no Results
// preserves the existing entry's Results — lifecycle records (attempted/outcome) and
// fact records (MergeSubsystemResults) are written by different call sites, and an
// outcome upsert must not erase the facts a subsystem already recorded.
func (st *State) RecordSubsystem(s SubsystemState) {
	for i, existing := range st.Subsystems {
		if existing.Name == s.Name {
			if s.Results == nil {
				s.Results = existing.Results
			}
			st.Subsystems[i] = s
			return
		}
	}
	st.Subsystems = append(st.Subsystems, s)
}

// MergeSubsystemResults merges fact key→value pairs into a subsystem's Results,
// creating the entry if the subsystem has no record yet and leaving every lifecycle
// field untouched. This is the write path for recorded facts ({domain.key} tokens).
func (st *State) MergeSubsystemResults(name string, results map[string]string) {
	if len(results) == 0 {
		return
	}
	for i := range st.Subsystems {
		if st.Subsystems[i].Name != name {
			continue
		}
		if st.Subsystems[i].Results == nil {
			st.Subsystems[i].Results = make(map[string]string, len(results))
		}
		for k, v := range results {
			st.Subsystems[i].Results[k] = v
		}
		return
	}
	st.Subsystems = append(st.Subsystems, SubsystemState{Name: name, Results: results})
}

// UpdateState does read-modify-write. The caller mutates individual fields
// only — never rebuild nested structs wholesale to avoid clobbering prior
// state written by other subsystems.
func UpdateState(rootDir string, fn func(*State)) error {
	st, err := ReadState(rootDir)
	if err != nil {
		return err
	}
	fn(st)
	return WriteState(rootDir, st)
}

// InitFromCI populates a CIState from a ci.CIContext.
func InitFromCI(ciCtx *ci.CIContext) CIState {
	ref := ciCtx.Branch
	if ref == "" {
		ref = ciCtx.Tag
	}
	return CIState{
		Provider:   ciCtx.Provider,
		PipelineID: ciCtx.PipelineID,
		Ref:        ref,
		Branch:     ciCtx.Branch,
		Tag:        ciCtx.Tag,
		SHA:        ciCtx.SHA,
	}
}
