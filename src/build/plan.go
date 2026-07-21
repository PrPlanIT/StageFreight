package build

import "github.com/PrPlanIT/StageFreight/src/config"

// OutputMode describes what the build produces.
type OutputMode string

const (
	OutputImage OutputMode = "image" // container image (push to registry or load to daemon)
	OutputLocal OutputMode = "local" // extract files to local filesystem
	OutputTar   OutputMode = "tar"   // export as tarball
)

// BuildPlan is the resolved execution plan for a build.
type BuildPlan struct {
	Steps []BuildStep
}

// BuildStep is a single build invocation.
type BuildStep struct {
	Name           string
	Dockerfile     string
	Context        string
	ContextDigest  string // content hash of the Dockerfile + build context (see HashBuildContext); folds source content into the build identity so a code change is not invisible to NormalizeBuildPlan
	Target         string
	Platforms      []string
	BuildArgs      map[string]string
	Labels         map[string]string // OCI labels injected via --label
	Tags           []string
	Output         OutputMode
	Extract        []ExtractRule    // artifact mode only
	Registries     []RegistryTarget // image mode only
	Load           bool             // --load into daemon
	Push           bool             // --push to registries
	MetadataFile   string           // temp file for buildx --metadata-file (digest capture)
	OCILayoutDir   string           // --output type=oci,dest=<dir> for content-store persistence; additive to Load/Push
	CacheFrom      []CacheRef       // --cache-from references
	CacheTo        []CacheRef       // --cache-to references
	SkippedTargets []TargetSkip     // registry targets excluded by when:, with the matcher's reason (narrated)
}

// TargetSkip records a registry target excluded at plan time and the matcher's
// reason. It is narrated so a "built but not distributed" outcome explains
// itself rather than looking like a bug. The Reason comes from the eligibility
// decision (config.MatchResult), never re-derived by a caller.
type TargetSkip struct {
	TargetID string
	Reason   string
}

// CacheRef is a structured build cache reference.
type CacheRef struct {
	Type      string // "registry", "local"
	Ref       string // registry ref or local path
	Mode      string // "max", "min" (export only)
	Direction string // "import" or "export" — determines local key (src vs dest)
}

// Flag renders the CacheRef as a buildx --cache-from/--cache-to value.
// Local cache uses src=/dest= keys. Registry cache uses ref=.
func (c CacheRef) Flag() string {
	var f string
	switch c.Type {
	case "local":
		if c.Direction == "export" {
			f = "type=local,dest=" + c.Ref
		} else {
			f = "type=local,src=" + c.Ref
		}
	default:
		f = "type=" + c.Type + ",ref=" + c.Ref
	}
	if c.Mode != "" {
		f += ",mode=" + c.Mode
	}
	return f
}

// ExtractRule defines a file to extract from a build container.
type ExtractRule struct {
	From string // path inside the container
	To   string // local destination path
}

// RegistryTarget is a resolved registry push destination.
type RegistryTarget struct {
	URL         string
	Path        string
	Tags        []string
	Credentials string                 // env var prefix for auth (e.g., "DOCKERHUB" → DOCKERHUB_USER/DOCKERHUB_PASS)
	Provider    string                 // registry vendor: dockerhub, ghcr, gitlab, jfrog, harbor, quay, gitea, generic
	Retention   config.RetentionPolicy // restic-style retention policy
	TagPatterns []string               // original unresolved tag templates for pattern matching during retention
	NativeScan  bool                   // trigger registry's own built-in scan after push (Harbor: built-in Trivy)

	// SigningProfile is the resolved trust profile published images are signed
	// under — the synthesized `legacy` default (implicit COSIGN_KEY signing) when
	// the target names no signing_profile. Resolved at lowering, consumed by the
	// publish-phase image signer.
	SigningProfile *config.ResolvedSigningProfile
}
