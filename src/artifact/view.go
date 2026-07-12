package artifact

// PublicationView is the canonical consumer-side join over an
// OutputsManifest (intent) and a ResultsManifest (execution facts). One
// PublicationView per (artifact, target, tag) tuple where execution
// recorded a push outcome — successful or otherwise.
//
// The view is the first read-model primitive in the v2 truth pipeline:
// instead of reading the dual-purpose v1 PublishManifest, consumers
// (security_scan, release_create, verify, etc.) consume []PublicationView
// produced by BuildPublicationViews. All join logic lives here, not in
// each consumer.
//
// Field provenance is explicit in the comments below: each field is sourced
// from intent OR observation. Derived fields are reconstructions over
// outcome existence, not stored state.
type PublicationView struct {
	// ── Identity (intent) ──────────────────────────────────────────────
	ArtifactID   ArtifactID // "<kind>:<name>" — the cross-domain join key
	ArtifactKind string
	ArtifactName string
	Version      string // from Artifact.Version; may be empty

	// ── Target coordinates (observation) ───────────────────────────────
	Host string
	Path string
	Tag  string

	// ── Push observation (results.outcomes[type=push]) ─────────────────
	PushStatus     OutcomeStatus
	Digest         string // immutable digest if known; empty if registry did not return one
	ObservedDigest string
	ObservedBy     string
	PushError      string // empty on success

	// ── Attestation observation (results.outcomes[type=attestation]) ──
	// SigningAttempted is derived: true iff any attestation outcome
	// exists for (ArtifactID, Host, Path, Tag), regardless of status.
	// Absence of an attestation outcome means signing was NOT attempted
	// — per the architectural invariant established in Phase 3.
	SigningAttempted bool
	Attestation      *AttestationOutcome

	// ── Joined intent metadata ────────────────────────────────────────
	ExpectedTags   []string     // all tags configured for this (host, path) registry target
	ExpectedCommit string       // OutputsManifest.Commit
	Requirements   Requirements // intent-side requirements for this target (sign, attest, sbom)
}

// Ref returns the canonical mutable image reference: host/path:tag.
func (v PublicationView) Ref() string {
	return v.Host + "/" + v.Path + ":" + v.Tag
}

// DigestRef returns the immutable reference (host/path@digest) when a
// digest is known, or empty string otherwise. Consumers that require an
// immutable target (e.g. security scanning) should check for empty before
// using.
func (v PublicationView) DigestRef() string {
	if v.Digest == "" {
		return ""
	}
	return v.Host + "/" + v.Path + "@" + v.Digest
}

// BuildPublicationViews produces the canonical join over outputs (intent)
// and results (observations). Returns one view per push outcome.
//
// Returns nil if either manifest is nil. If outputs contains no matching
// intent for a recorded artifact_id, that artifact's outcomes are still
// surfaced — the cross-domain validation already happened at
// ResultsBuilder.Build time; here we only do enrichment.
//
// Per-target attestation outcomes are joined to push outcomes by exact
// (artifact_id, host, path, tag) tuple. If multiple attestation outcomes
// exist for the same tuple (e.g. retry behavior), the last one in append
// order wins — that represents the final attempted state.
func BuildPublicationViews(outputs *OutputsManifest, results *ResultsManifest) []PublicationView {
	if outputs == nil || results == nil {
		return nil
	}

	// Build intent lookup tables. Keyed by artifact_id; inner map keyed
	// by "host/path" so multiple tags share one registry intent.
	type registryIntent struct {
		tags         []string
		requirements Requirements
	}
	type artifactIntent struct {
		kind       string
		name       string
		version    string
		registries map[string]registryIntent
	}
	// Intent lookup keyed by typed ArtifactID — the cross-domain join key.
	// Inner registry map is keyed by a presentation cluster (host/path),
	// not an identity — registries are not artifacts and this map is only
	// used to enrich views with intent metadata, never to identify.
	intents := make(map[ArtifactID]artifactIntent, len(outputs.Artifacts))
	for _, a := range outputs.Artifacts {
		ai := artifactIntent{
			kind:       a.Kind,
			name:       a.Name,
			version:    a.Version,
			registries: make(map[string]registryIntent),
		}
		for _, t := range a.Targets {
			if t.Kind != "registry" || t.Registry == nil {
				continue
			}
			tagsCopy := make([]string, len(t.Registry.Tags))
			copy(tagsCopy, t.Registry.Tags)
			ai.registries[t.Registry.Host+"/"+t.Registry.Path] = registryIntent{
				tags:         tagsCopy,
				requirements: t.Requirements,
			}
		}
		intents[a.ID] = ai
	}

	// Index attestation outcomes by (artifact_id, host, path, tag) for
	// O(1) join during push iteration. Last attestation per key wins.
	type tgtKey struct {
		artifactID          ArtifactID
		host, path, tag     string
	}
	attestations := make(map[tgtKey]*AttestationOutcome)
	for _, r := range results.Results {
		for i := range r.Outcomes {
			o := &r.Outcomes[i]
			if o.Type != OutcomeTypeAttestation || o.Attestation == nil {
				continue
			}
			attestations[tgtKey{r.ArtifactID, o.Target.Host, o.Target.Path, o.Target.Tag}] = o.Attestation
		}
	}

	var views []PublicationView
	for _, r := range results.Results {
		ai := intents[r.ArtifactID] // zero value if missing — intent fields stay empty
		for _, o := range r.Outcomes {
			if o.Type != OutcomeTypePush || o.Push == nil {
				continue
			}

			view := PublicationView{
				ArtifactID:     r.ArtifactID,
				ArtifactKind:   r.Kind,
				ArtifactName:   r.ArtifactName,
				Version:        ai.version,
				Host:           o.Target.Host,
				Path:           o.Target.Path,
				Tag:            o.Target.Tag,
				PushStatus:     o.Push.Status,
				Digest:         o.Push.Digest,
				ObservedDigest: o.Push.ObservedDigest,
				ObservedBy:     o.Push.ObservedBy,
				PushError:      o.Push.Error,
				ExpectedCommit: outputs.Commit,
			}

			if ri, ok := ai.registries[o.Target.Host+"/"+o.Target.Path]; ok {
				view.ExpectedTags = ri.tags
				view.Requirements = ri.requirements
			}

			key := tgtKey{r.ArtifactID, o.Target.Host, o.Target.Path, o.Target.Tag}
			if att, ok := attestations[key]; ok {
				view.SigningAttempted = true
				view.Attestation = att
			}

			views = append(views, view)
		}
	}

	return views
}

// BinaryExecutionView is the canonical consumer-side join over an
// OutputsManifest (intent) and a ResultsManifest (execution facts) for
// binary artifacts. Each successful binary build produces one view.
//
// Binary artifacts are un-targeted by design (Q2): there are no Target
// coordinates, no registry, no remote ref. The build artifact IS the
// truth. Distribution destinations are decided at a later layer.
//
// Per the architectural rule: "Views are projections, not resolvers."
// BinaryExecutionView does NOT include archive information, does NOT
// resolve dependencies, does NOT walk into other views. Consumers that
// need cross-domain context (e.g., archives that wrap this binary) must
// query the relevant view builder separately and join at the consumer
// level. Sibling artifact relationships are referenced by ArtifactID only.
type BinaryExecutionView struct {
	// Identity (intent)
	ArtifactID   ArtifactID
	ArtifactKind string // always "binary"
	ArtifactName string
	Version      string

	// Descriptor (intent)
	OS        string
	Arch      string
	Path      string
	Toolchain string

	// Build observation (outcome)
	BuildStatus OutcomeStatus
	SHA256      string
	Size        int64
	BuildID     string
	BuildError  string

	// Provenance
	ExpectedCommit string
}

// BuildBinaryExecutionViews produces one view per recorded binary_build
// outcome. Returns nil for nil inputs. Surfaces failed builds too — the
// view layer is broad; consumers narrow by filtering on BuildStatus.
func BuildBinaryExecutionViews(outputs *OutputsManifest, results *ResultsManifest) []BinaryExecutionView {
	if outputs == nil || results == nil {
		return nil
	}

	type binaryIntent struct {
		name      string
		version   string
		os        string
		arch      string
		path      string
		toolchain string
	}
	intents := make(map[ArtifactID]binaryIntent, len(outputs.Artifacts))
	for _, a := range outputs.Artifacts {
		if a.Kind != "binary" || a.Binary == nil {
			continue
		}
		intents[a.ID] = binaryIntent{
			name:      a.Name,
			version:   a.Version,
			os:        a.Binary.OS,
			arch:      a.Binary.Arch,
			path:      a.Binary.Path,
			toolchain: a.Binary.Toolchain,
		}
	}

	var views []BinaryExecutionView
	for _, r := range results.Results {
		if r.Kind != "binary" {
			continue
		}
		bi := intents[r.ArtifactID]
		for _, o := range r.Outcomes {
			if o.Type != OutcomeTypeBinaryBuild || o.Binary == nil {
				continue
			}
			views = append(views, BinaryExecutionView{
				ArtifactID:     r.ArtifactID,
				ArtifactKind:   r.Kind,
				ArtifactName:   r.ArtifactName,
				Version:        bi.version,
				OS:             bi.os,
				Arch:           bi.arch,
				Path:           bi.path,
				Toolchain:      bi.toolchain,
				BuildStatus:    o.Binary.Status,
				SHA256:         o.Binary.SHA256,
				Size:           o.Binary.Size,
				BuildID:        o.Binary.BuildID,
				BuildError:     o.Binary.Error,
				ExpectedCommit: outputs.Commit,
			})
		}
	}
	return views
}

// ArchiveExecutionView is the canonical join for archive artifacts. One
// view per archive_build outcome. Archive is a sibling of its source
// binaries (Q3) — Sources references binary ArtifactIDs by string only,
// never embedding binary fields. Sources is treated as an unordered set
// by consumers; on-disk serialization sorts it for determinism.
//
// Per the same projection-not-resolver rule as BinaryExecutionView, this
// view does NOT walk into BinaryExecutionViews for its Sources. Consumers
// that need binary details must look them up separately.
type ArchiveExecutionView struct {
	// Identity (intent)
	ArtifactID   ArtifactID
	ArtifactKind string // always "archive"
	ArtifactName string
	Version      string

	// Descriptor (intent)
	Format string
	Path   string
	// Set is the binary-archive target id that produced this archive (empty for an internal
	// transport). Distribution consumers scope to their `archives:` selector by matching it.
	Set string

	// Build observation (outcome)
	BuildStatus OutcomeStatus
	SHA256      string
	Size        int64
	BuildError  string

	// Sibling reference — semantically unordered set of binary ArtifactIDs.
	// Consumers MUST NOT depend on the order of this slice.
	Sources []ArtifactID

	// Provenance
	ExpectedCommit string
}

// BuildArchiveExecutionViews produces one view per recorded archive_build
// outcome. Returns nil for nil inputs. Like BinaryExecutionView, surfaces
// failures broadly; consumers filter on BuildStatus.
func BuildArchiveExecutionViews(outputs *OutputsManifest, results *ResultsManifest) []ArchiveExecutionView {
	if outputs == nil || results == nil {
		return nil
	}

	type archiveIntent struct {
		name    string
		version string
		format  string
		path    string
		set     string
	}
	intents := make(map[ArtifactID]archiveIntent, len(outputs.Artifacts))
	for _, a := range outputs.Artifacts {
		if a.Kind != "archive" || a.Archive == nil {
			continue
		}
		intents[a.ID] = archiveIntent{
			name:    a.Name,
			version: a.Version,
			format:  a.Archive.Format,
			path:    a.Archive.Path,
			set:     a.Archive.Set,
		}
	}

	var views []ArchiveExecutionView
	for _, r := range results.Results {
		if r.Kind != "archive" {
			continue
		}
		ai := intents[r.ArtifactID]
		for _, o := range r.Outcomes {
			if o.Type != OutcomeTypeArchive || o.Archive == nil {
				continue
			}
			sources := make([]ArtifactID, len(o.Archive.Sources))
			copy(sources, o.Archive.Sources)
			views = append(views, ArchiveExecutionView{
				ArtifactID:     r.ArtifactID,
				ArtifactKind:   r.Kind,
				ArtifactName:   r.ArtifactName,
				Version:        ai.version,
				Format:         ai.format,
				Path:           ai.path,
				Set:            ai.set,
				BuildStatus:    o.Archive.Status,
				SHA256:         o.Archive.SHA256,
				Size:           o.Archive.Size,
				BuildError:     o.Archive.Error,
				Sources:        sources,
				ExpectedCommit: outputs.Commit,
			})
		}
	}
	return views
}
