package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/credentials"
	"github.com/PrPlanIT/StageFreight/src/diag"
	"github.com/PrPlanIT/StageFreight/src/forge"
	"github.com/PrPlanIT/StageFreight/src/gitstate"
	"github.com/PrPlanIT/StageFreight/src/gitver"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/registry"
	"github.com/PrPlanIT/StageFreight/src/release"
	"github.com/PrPlanIT/StageFreight/src/retention"
	"github.com/PrPlanIT/StageFreight/src/sign/provision"
	"github.com/spf13/cobra"
)

// ReleaseCreateRequest is the explicit input contract for RunReleaseCreate.
// Cobra command fills this from flags; CI runner fills it from config/ciCtx.
// Ctx is inside the request (matches docker.Request pattern).
type ReleaseCreateRequest struct {
	Ctx             context.Context
	RootDir         string
	Config          *config.Config
	Tag             string
	Ref             string // commit/branch the forge mints a synthesized tag from (e.g. dev-{sha8} on a push)
	Name            string
	NotesFile       string
	SecuritySummary string
	Draft           bool
	Prerelease      bool
	Assets          []string
	RegistryLinks   bool
	CatalogLinks    bool
	SkipSync        bool
	ReadOnly        bool // run_from: read-only mode — evaluate + narrate but do not mutate
	Verbose         bool
	Writer          io.Writer
}

var (
	rcTag             string
	rcName            string
	rcNotesFile       string
	rcSecuritySummary string
	rcDraft           bool
	rcPrerelease      bool
	rcAssets          []string
	rcRegistryLinks   bool
	rcCatalogLinks    bool
	rcSkipSync        bool
)

var releaseCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a release on the forge and sync to targets",
	Long: `Create a release on the detected forge (GitLab, GitHub, Gitea)
with generated or provided release notes.

Optionally uploads assets (scan artifacts, SBOMs) and adds
registry image links. Syncs to configured remote release targets
unless --skip-sync is set.`,
	RunE: runReleaseCreate,
}

func init() {
	releaseCreateCmd.Flags().StringVar(&rcTag, "tag", "", "release tag (default: detected from git)")
	releaseCreateCmd.Flags().StringVar(&rcName, "name", "", "release name (default: tag)")
	releaseCreateCmd.Flags().StringVar(&rcNotesFile, "notes", "", "path to release notes markdown file")
	releaseCreateCmd.Flags().StringVar(&rcSecuritySummary, "security-summary", "", "path to security output directory (reads summary.md)")
	releaseCreateCmd.Flags().BoolVar(&rcDraft, "draft", false, "create as draft release")
	releaseCreateCmd.Flags().BoolVar(&rcPrerelease, "prerelease", false, "mark as prerelease")
	releaseCreateCmd.Flags().StringSliceVar(&rcAssets, "asset", nil, "files to attach to release (repeatable)")
	releaseCreateCmd.Flags().BoolVar(&rcRegistryLinks, "registry-links", true, "add registry image links to release")
	releaseCreateCmd.Flags().BoolVar(&rcCatalogLinks, "catalog-links", true, "add GitLab Catalog link to release")
	releaseCreateCmd.Flags().BoolVar(&rcSkipSync, "skip-sync", false, "skip syncing to other forges")

	releaseCmd.AddCommand(releaseCreateCmd)
}

// actionResult tracks the outcome of a single release action.
type actionResult struct {
	Name string
	OK   bool
	Err  error
}

// releaseReport collects all release action outcomes for rendering.
type releaseReport struct {
	Tag, Forge, URL string
	Assets          []actionResult
	Links           []actionResult
	Tags            []actionResult
}

func runReleaseCreate(cmd *cobra.Command, args []string) error {
	rootDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	// Apply config defaults when CLI flags are not explicitly set, then merge into request.
	secSummary := rcSecuritySummary
	if !cmd.Flags().Changed("security-summary") && cfg.Release.SecuritySummary != "" {
		secSummary = cfg.Release.SecuritySummary
	}
	regLinks := rcRegistryLinks
	if !cmd.Flags().Changed("registry-links") {
		regLinks = cfg.Release.RegistryLinks
	}
	catLinks := rcCatalogLinks
	if !cmd.Flags().Changed("catalog-links") {
		catLinks = cfg.Release.CatalogLinks
	}

	return RunReleaseCreate(ReleaseCreateRequest{
		Ctx:             cmd.Context(),
		RootDir:         rootDir,
		Config:          cfg,
		Tag:             rcTag,
		Name:            rcName,
		NotesFile:       rcNotesFile,
		SecuritySummary: secSummary,
		Draft:           rcDraft,
		Prerelease:      rcPrerelease,
		Assets:          rcAssets,
		RegistryLinks:   regLinks,
		CatalogLinks:    catLinks,
		SkipSync:        rcSkipSync,
		Verbose:         verbose,
		Writer:          os.Stdout,
	})
}

// RunReleaseCreate executes the full release creation pipeline from an explicit request.
// All inputs are taken from req — no package-level vars are referenced.
func RunReleaseCreate(req ReleaseCreateRequest) error {
	rootDir := req.RootDir
	ctx := req.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	w := req.Writer
	if w == nil {
		w = os.Stdout
	}
	color := output.UseColor()

	// Detect version for tag
	versionInfo, err := build.DetectVersion(rootDir, req.Config)
	if err != nil {
		return fmt.Errorf("detecting version: %w", err)
	}

	tag := req.Tag
	if tag == "" {
		tag = "v" + versionInfo.Version
	}
	name := req.Name
	if name == "" {
		name = tag
	}

	// Load security summary if provided
	var secTile, secBody string
	if req.SecuritySummary != "" {
		summaryPath := req.SecuritySummary + "/summary.md"
		data, err := os.ReadFile(summaryPath)
		if err != nil {
			// Not fatal — security scan may have been skipped
			if req.Verbose {
				fmt.Fprintf(os.Stderr, "note: no security summary at %s: %v\n", summaryPath, err)
			}
		} else {
			content := strings.TrimSpace(string(data))
			if content != "" {
				parts := strings.SplitN(content, "\n", 2)
				secTile = strings.TrimSpace(parts[0])
				secBody = content
			}
		}
	}

	// ── release_create v2 invariant ────────────────────────────────────
	// ArtifactID is the only join key across all artifact views.
	// Any other derived key is strictly presentation-only and must not
	// participate in joins, lookups, or graph construction.
	//
	// Forbidden patterns (caught by TestNoIdentityReconstructionPatterns):
	//   - string concat of fields as identity (e.g. name + "-" + os + "-" + arch)
	//   - composite map keys derived from fields (BuildID/OS/Arch tuples)
	//   - parsing of artifact names to derive type/platform
	//   - any lookup keyed by string when the key represents artifact identity
	// ────────────────────────────────────────────────────────────────────
	currentTag := os.Getenv("CI_COMMIT_TAG")

	outputs, outputsErr := artifact.ReadOutputsManifest(rootDir)
	results, resultsErr := artifact.ReadResultsManifest(rootDir)
	var imageRows []release.ImageRow
	var downloadRows []release.BinaryRow
	var manifestAssets []string
	var verify *release.Verification

	switch {
	case outputsErr == nil && resultsErr == nil:
		// Truth mode: v2 outputs + results both present.
		publicationViews := artifact.BuildPublicationViews(outputs, results)
		archiveViews := artifact.BuildArchiveExecutionViews(outputs, results)
		binaryViews := artifact.BuildBinaryExecutionViews(outputs, results)

		credResolver := func(prefix string) (string, string) {
			cred := credentials.ResolvePrefix(prefix)
			if cred.Kind == credentials.SecretPassword {
				diag.Warn("credentials %s: authenticating with %s — consider using %s_TOKEN instead (scoped, revocable)",
					prefix, cred.SecretEnv, strings.ToUpper(prefix))
			}
			return cred.User, cred.Secret
		}

		// Filter to successful pushes — only verified-publishable images
		// participate in release. Failed pushes legitimately surface in
		// PublicationView but cannot be published.
		var pubViews []artifact.PublicationView
		for _, v := range publicationViews {
			if v.PushStatus == artifact.OutcomeSuccess {
				pubViews = append(pubViews, v)
			}
		}

		if len(pubViews) > 0 {
			// Build verify targets — primitives + typed ArtifactID for join-back.
			verifyTargets := make([]registry.ImageVerifyTarget, 0, len(pubViews))
			discoveryTargets := make([]registry.ArtifactDiscoveryTarget, 0, len(pubViews))
			for _, v := range pubViews {
				credRef := credentialRefForHost(req.Config, v.Host)
				verifyTargets = append(verifyTargets, registry.ImageVerifyTarget{
					ArtifactID:     v.ArtifactID,
					Host:           v.Host,
					Path:           v.Path,
					Tag:            v.Tag,
					ExpectedDigest: v.Digest,
					CredentialRef:  credRef,
				})
				if v.Digest != "" {
					discoveryTargets = append(discoveryTargets, registry.ArtifactDiscoveryTarget{
						ArtifactID:    v.ArtifactID,
						Host:          v.Host,
						Path:          v.Path,
						Digest:        v.Digest,
						CredentialRef: credRef,
					})
				}
			}

			verifyResults, verifyErr := registry.VerifyImages(ctx, verifyTargets, credResolver)
			if verifyErr != nil {
				return fmt.Errorf("verifying published images: %w", verifyErr)
			}
			for _, r := range verifyResults {
				if !r.Verified {
					return fmt.Errorf("published image %s/%s:%s failed remote verification: %v",
						r.Host, r.Path, r.Tag, r.Err)
				}
			}

			// Artifact discovery keyed by ArtifactID — typed join key, no
			// reconstructed lookup keys.
			artifactMap := registry.DiscoverAllArtifacts(ctx, discoveryTargets, credResolver)

			// Group publication views into ImageRow per (Host, Path).
			// This grouping key is presentation-only — it is never used to
			// retrieve or identify an artifact. The artifact identity is
			// always the ArtifactID, carried separately.
			type imageGroupKey struct{ host, path string }
			type pendingImageGroup struct {
				artifactID artifact.ArtifactID
				host       string
				path       string
				provider   string
				seen       map[string]bool
				tagList    []string
				digest     string
				sbom       string
				prov       string
				sig        string
			}
			groupIndex := make(map[imageGroupKey]*pendingImageGroup)
			var groupOrder []imageGroupKey
			for _, v := range pubViews {
				k := imageGroupKey{host: v.Host, path: v.Path}
				g, exists := groupIndex[k]
				if !exists {
					g = &pendingImageGroup{
						artifactID: v.ArtifactID,
						host:       v.Host,
						path:       v.Path,
						provider:   providerFromHost(v.Host),
						seen:       make(map[string]bool),
					}
					groupIndex[k] = g
					groupOrder = append(groupOrder, k)
				}
				if !g.seen[v.Tag] {
					g.seen[v.Tag] = true
					g.tagList = append(g.tagList, v.Tag)
				}
				if v.Digest != "" && g.digest == "" {
					g.digest = v.Digest
					if links, ok := artifactMap[v.ArtifactID]; ok {
						g.sbom = links.SBOM
						g.prov = links.Provenance
						g.sig = links.Signature
					}
				}
			}

			// Sort groups by (provider, host) — presentation order.
			sort.SliceStable(groupOrder, func(i, j int) bool {
				gi, gj := groupIndex[groupOrder[i]], groupIndex[groupOrder[j]]
				if gi.provider != gj.provider {
					return gi.provider < gj.provider
				}
				return gi.host < gj.host
			})

			imageRows = make([]release.ImageRow, 0, len(groupOrder))
			for _, k := range groupOrder {
				g := groupIndex[k]
				rt := registry.ResolvedRegistryTarget{
					Provider: g.provider,
					Host:     g.host,
					Path:     g.path,
					Tags:     g.tagList,
				}
				tags := make([]release.ResolvedTag, 0, len(g.tagList))
				for _, t := range g.tagList {
					tags = append(tags, release.ResolvedTag{
						Name: t,
						URL:  rt.TagURL(t),
					})
				}
				var digestRef string
				if g.digest != "" {
					digestRef = g.host + "/" + g.path + "@" + g.digest
				}
				imageRows = append(imageRows, release.ImageRow{
					RegistryLabel: rt.DisplayName(),
					RegistryURL:   rt.RepoURL(),
					ImageRef:      rt.ImageRef(),
					Tags:          tags,
					DigestRef:     digestRef,
					SBOM:          g.sbom,
					Provenance:    g.prov,
					Signature:     g.sig,
				})
			}
		}

		// Archive + binary download rows. The cross-domain join is by
		// ArtifactID exact equality: archives reference source binary
		// ArtifactIDs in Sources; uncovered binaries are those whose
		// ArtifactID is not in any archive's Sources set.
		//
		// Assets are collected as (row, path, ArtifactID, kind) tuples so
		// the cross-kind canonicalization sort below works directly on
		// the typed identity — no ArtifactName→ArtifactID reverse lookup.
		// Carrying identity alongside the row from construction is the
		// invariant: identity is propagated unchanged, never re-derived.
		binaryByID := make(map[artifact.ArtifactID]artifact.BinaryExecutionView, len(binaryViews))
		for _, bv := range binaryViews {
			binaryByID[bv.ArtifactID] = bv
		}
		coveredIDs := make(map[artifact.ArtifactID]struct{})
		var assets []releaseAsset

		// Successful archives via the shared distribution helper (same source of
		// truth used by kind: generic-package). coveredIDs and the row's display
		// platform stay release-local; the helper only supplies the typed asset list.
		for _, a := range artifact.SuccessfulArchiveAssets(archiveViews) {
			for _, sourceID := range a.Sources {
				coveredIDs[sourceID] = struct{}{}
			}
			assets = append(assets, releaseAsset{
				Kind:       "archive",
				ArtifactID: a.ArtifactID,
				AssetPath:  a.Path,
				Row: release.BinaryRow{
					Name:     a.Name,
					Platform: archivePlatform(a.Sources, binaryByID),
					Size:     a.Size,
					SHA256:   a.SHA256,
				},
			})
		}
		for _, bv := range binaryViews {
			if bv.BuildStatus != artifact.OutcomeSuccess {
				continue
			}
			if _, covered := coveredIDs[bv.ArtifactID]; covered {
				continue
			}
			assets = append(assets, releaseAsset{
				Kind:       "binary",
				ArtifactID: bv.ArtifactID,
				AssetPath:  bv.Path,
				Row: release.BinaryRow{
					Name:     bv.ArtifactName,
					Platform: bv.OS + "/" + bv.Arch,
					Size:     bv.Size,
					SHA256:   bv.SHA256,
				},
			})
		}

		// Cross-kind canonicalization sort. The key is (Kind, ArtifactID)
		// directly on the typed identity field — no name lookup, no
		// reconstruction. Trap-4 boundary: assets cross artifact kinds and
		// must be deterministically ordered before external publication.
		sort.SliceStable(assets, func(i, j int) bool {
			if assets[i].Kind != assets[j].Kind {
				return assets[i].Kind < assets[j].Kind
			}
			return assets[i].ArtifactID < assets[j].ArtifactID
		})

		downloadRows = make([]release.BinaryRow, len(assets))
		manifestAssets = make([]string, len(assets))
		for i, a := range assets {
			downloadRows[i] = a.Row
			manifestAssets[i] = a.AssetPath
		}

		// Detached blob signatures (e.g. SHA256SUMS.sig) — manifest-sourced from the
		// blob_signature outcomes — upload as release assets so consumers can verify.
		// They are NOT added to the Downloads table (no platform/size/checksum); the
		// Verification section presents them. Without this, a produced signature is
		// stranded in DistDir and never reaches the release.
		for _, s := range artifact.SuccessfulBlobSignatureAssets(results) {
			manifestAssets = append(manifestAssets, s.Path)
		}

		// Publish the canonical public trust anchor (Tier-0 auto-provisioned key)
		// and the Verification disclosure: attach cosign.pub as a release asset and
		// state the assurance tier explicitly. Anchor + fingerprint come from the
		// state dir; the verify recipe + tier disclosure render in the notes.
		if v, anchorPath := buildVerification(req.Config.SigningSetup, results, rootDir); v != nil {
			verify = v
			if anchorPath != "" {
				manifestAssets = append(manifestAssets, anchorPath)
			}
		}

	case errors.Is(outputsErr, artifact.ErrOutputsManifestNotFound) ||
		errors.Is(resultsErr, artifact.ErrResultsManifestNotFound):
		// No v2 truth artifacts — fallback to config targets for image rows
		// (local dev, manual release). Binary/archive download rows are not
		// available in this mode; they require the build pipeline to have run.
		imageRows = buildImageRowsFromConfig(req.Config, currentTag, versionInfo)

	default:
		// One of the manifests was present but invalid (checksum mismatch,
		// parse error). Surface the error rather than silently degrading.
		if outputsErr != nil {
			return fmt.Errorf("outputs manifest: %w", outputsErr)
		}
		return fmt.Errorf("results manifest: %w", resultsErr)
	}

	// Generate or load release notes
	var notes string
	if req.NotesFile != "" {
		data, err := os.ReadFile(req.NotesFile)
		if err != nil {
			return fmt.Errorf("reading notes file: %w", err)
		}
		notes = string(data)
	} else {
		sha := versionInfo.SHA
		if len(sha) > 8 {
			sha = sha[:8]
		}
		// Collect release tag patterns from versioning tag sources
		var tagPatterns []string
		for _, ts := range req.Config.Versioning.TagSources {
			tagPatterns = append(tagPatterns, ts.Pattern)
		}

		// A synthesized channel tag (e.g. dev-{sha8}) is not yet a git ref — it is
		// minted by CreateRelease via Ref. For the changelog range, fall back to the
		// build commit, which points at the same place and always resolves. Existing
		// tags (stable releases) keep their tag semantics, so behavior is unchanged.
		toRef := tag
		if req.Ref != "" {
			if repo, oerr := gitstate.OpenRepo(rootDir); oerr == nil {
				if _, rerr := gitstate.ResolveRef(repo, tag); rerr != nil {
					toRef = req.Ref
				}
			}
		}

		input := release.NotesInput{
			RepoDir:      rootDir,
			ToRef:        toRef,
			TagPatterns:  tagPatterns,
			SecurityTile: secTile,
			SecurityBody: secBody,
			Version:      versionInfo.Version,
			SHA:          sha,
			IsPrerelease: versionInfo.IsPrerelease,
			Images:       imageRows,
			Downloads:    downloadRows,
			Verify:       verify,
		}
		notes, err = release.GenerateNotes(input)
		if err != nil {
			return fmt.Errorf("generating notes: %w", err)
		}
	}

	// Detect forge from git remote
	remoteURL, err := detectRemoteURL(rootDir)
	if err != nil {
		return fmt.Errorf("detecting remote: %w", err)
	}

	provider := forge.DetectProvider(remoteURL)
	if provider == forge.Unknown {
		return fmt.Errorf("could not detect forge from remote URL: %s", remoteURL)
	}

	// Create forge client
	forgeClient, err := newForgeClient(provider, remoteURL)
	if err != nil {
		return err
	}

	// Collect release targets from config. The "active" release target is the
	// first non-remote release whose when: matches THIS build's event, so a dev
	// channel rolls latest-dev on a push while a stable target rolls latest on a
	// tag — even when both are configured.
	primaryRelease := activeReleaseTarget(req.Config)
	remoteReleases := findRemoteReleaseTargets(req.Config)

	// ── Collect all results ──
	start := time.Now()
	report := releaseReport{
		Tag:   tag,
		Forge: string(provider),
	}

	// Create release on primary forge.
	// In read-only mode: narrate but do not mutate.
	if req.ReadOnly {
		fmt.Fprintf(w, "\n    [read-only] would create release %s on %s\n", tag, string(provider))
		fmt.Fprintf(w, "    [read-only] notes: %d bytes, %d assets\n\n", len(notes), len(req.Assets))
		return nil
	}

	rel, createErr := forgeClient.CreateRelease(ctx, forge.ReleaseOptions{
		TagName:     tag,
		Ref:         req.Ref, // mint the tag at this commit when it doesn't already exist (synthesized dev tags)
		Name:        name,
		Description: notes,
		Draft:       req.Draft,
		Prerelease:  req.Prerelease,
	})
	if createErr != nil {
		return fmt.Errorf("creating release: %w", createErr)
	}
	report.URL = rel.URL

	// Upload assets: manifest artifacts (binaries/archives) + explicit --asset flags.
	allAssets := append(manifestAssets, req.Assets...)
	for _, assetPath := range allAssets {
		assetName := filepath.Base(assetPath)

		// Defensive boundary: NEVER publish private signing-key material, no matter
		// how it reached the asset list. Structurally only public signatures are
		// added; this guards against a future regression at the external edge.
		if provision.IsPrivateKeyPath(assetPath) {
			fmt.Fprintf(os.Stderr, "refusing to upload signing key material as a release asset: %s\n", assetName)
			report.Assets = append(report.Assets, actionResult{Name: assetName, Err: fmt.Errorf("blocked: private key material")})
			continue
		}

		if err := forgeClient.UploadAsset(ctx, rel.ID, forge.Asset{
			Name:     assetName,
			FilePath: assetPath,
		}); err != nil {
			report.Assets = append(report.Assets, actionResult{Name: assetName, Err: err})
			fmt.Fprintf(os.Stderr, "warning: failed to upload %s: %v\n", assetPath, err)
		} else {
			report.Assets = append(report.Assets, actionResult{Name: assetName, OK: true})
		}
	}

	// Add registry image links (from kind: registry targets, deduplicate by URL)
	registryTargets := pipeline.CollectTargetsByKind(req.Config, "registry")
	if req.RegistryLinks && len(registryTargets) > 0 {
		linkedURLs := make(map[string]bool)
		for _, t := range registryTargets {
			resolved, resolveErr := config.ResolveRegistryForTarget(t, req.Config.Registries, req.Config.Vars)
			if resolveErr != nil {
				report.Links = append(report.Links, actionResult{Name: t.ID, Err: resolveErr})
				continue
			}
			regProvider := resolved.Provider
			if regProvider == "" {
				regProvider = build.DetectProvider(resolved.URL)
			}
			if p, err := registry.CanonicalProvider(regProvider); err == nil {
				regProvider = p
			} else {
				regProvider = "generic"
			}

			link := buildRegistryLinkFromTarget(resolved.URL, resolved.Path, tag, regProvider)
			if linkedURLs[link.URL] {
				continue
			}
			linkedURLs[link.URL] = true

			if err := forgeClient.AddReleaseLink(ctx, rel.ID, link); err != nil {
				report.Links = append(report.Links, actionResult{Name: link.Name, Err: err})
				fmt.Fprintf(os.Stderr, "warning: failed to add registry link for %s: %v\n", resolved.URL, err)
			} else {
				report.Links = append(report.Links, actionResult{Name: link.Name, OK: true})
			}
		}
	}

	// Add GitLab Catalog link (from kind: gitlab-component targets)
	if req.CatalogLinks && provider == forge.GitLab {
		for _, t := range req.Config.Targets {
			if t.Kind == "gitlab-component" && t.Catalog {
				catalogLink := buildCatalogLink(remoteURL, tag)
				if catalogLink.URL != "" {
					if err := forgeClient.AddReleaseLink(ctx, rel.ID, catalogLink); err != nil {
						report.Links = append(report.Links, actionResult{Name: catalogLink.Name, Err: err})
						fmt.Fprintf(os.Stderr, "warning: failed to add catalog link: %v\n", err)
					} else {
						report.Links = append(report.Links, actionResult{Name: catalogLink.Name, OK: true})
					}
				}
				break // only one catalog link
			}
		}
	}

	// Auto-tagging: create rolling git tags for configured aliases on primary release target.
	// These are lightweight git tags (not releases) — they point at the release tag.
	if primaryRelease != nil && len(primaryRelease.Aliases) > 0 {
		currentTag := os.Getenv("CI_COMMIT_TAG")
		// CRITICAL:
		// tag_sources as map is ONLY for when.git_tags lookup on target conditions.
		// DO NOT reuse this for version selection — that would reintroduce
		// global filtering and break the search-path invariant.
		tagPatternMap := tagPatternLookupForConditionsOnly(req.Config.Versioning.TagSources)
		// Check when conditions on the primary release target
		if targetWhenMatches(*primaryRelease, currentTag, tagPatternMap, req.Config.Matchers.Branches) {
			rollingTags := gitver.ResolveTags(primaryRelease.Aliases, versionInfo)
			for _, rt := range rollingTags {
				if rt == tag || rt == "" {
					continue
				}
				// Try create, fallback to delete+recreate on conflict
				err := forgeClient.CreateTag(ctx, rt, tag)
				if err != nil {
					// Rolling tag may already exist — delete then recreate
					_ = forgeClient.DeleteTag(ctx, rt)
					err = forgeClient.CreateTag(ctx, rt, tag)
					if err != nil {
						report.Tags = append(report.Tags, actionResult{Name: rt, Err: err})
						fmt.Fprintf(os.Stderr, "warning: rolling tag %s: %v\n", rt, err)
						continue
					}
				}
				report.Tags = append(report.Tags, actionResult{Name: rt, OK: true})

				// Channel aliases (target has a Tag pattern) are also refreshed as
				// pullable RELEASES with assets, so e.g. latest-dev resolves at
				// /-/releases/latest-dev/downloads/... Stable targets (no Tag) keep
				// rolling-tag-only behavior, unchanged.
				if primaryRelease.Tag != "" {
					if err := refreshRollingRelease(ctx, forgeClient, rt, req.Ref, rt, notes, primaryRelease.Prerelease, allAssets); err != nil {
						report.Tags = append(report.Tags, actionResult{Name: rt + " (release)", Err: err})
						fmt.Fprintf(os.Stderr, "warning: rolling release %s: %v\n", rt, err)
					}
				}
			}
		}
	}

	elapsed := time.Since(start)

	// ── Release section ──
	overallStatus := "created"
	overallIcon := "success"
	if hasActionFailures(report.Assets) || hasActionFailures(report.Links) || hasActionFailures(report.Tags) {
		overallStatus = "partial"
		overallIcon = "skipped" // yellow icon
	}

	output.SectionStart(w, "sf_release", "Release")
	sec := output.NewSection(w, "Release", elapsed, color)
	sec.Row("%s  →  %s   %s  %s", tag, provider, output.StatusIcon(overallIcon, color), overallStatus)
	sec.Row("%s", report.URL)

	if len(report.Assets) > 0 || len(report.Links) > 0 || len(report.Tags) > 0 {
		sec.Row("")
		if len(report.Assets) > 0 {
			renderCheckpoint(sec, color, "assets", report.Assets)
		}
		if len(report.Links) > 0 {
			renderCheckpoint(sec, color, "links", report.Links)
		}
		if len(report.Tags) > 0 {
			renderCheckpoint(sec, color, "tags", report.Tags)
		}
	}

	sec.Close()
	output.SectionEnd(w, "sf_release")

	// ── Release projection ──
	// Sources declare destinations. Targets declare production + optional overrides.
	// Precedence per mirror:
	//   1. Explicit release target with mirror: <id> → use target behavior
	//   2. Mirror with sync.releases: true → default projection from canonical release
	//   3. Neither → skip
	if !req.SkipSync {
		currentTag := os.Getenv("CI_COMMIT_TAG")
		var syncResults []actionResult
		syncStart := time.Now()

		// Collect mirrors that have explicit target overrides.
		overriddenMirrors := make(map[string]bool)
		for _, t := range remoteReleases {
			if t.Mirror != "" {
				overriddenMirrors[t.Mirror] = true
			}
		}

		// CRITICAL:
		// tag_sources as map is ONLY for when.git_tags lookup on target conditions.
		// DO NOT reuse this for version selection — that would reintroduce
		// global filtering and break the search-path invariant.
		remoteTagPatternMap := tagPatternLookupForConditionsOnly(req.Config.Versioning.TagSources)

		// Path 1: Explicit target overrides.
		for _, t := range remoteReleases {
			if !targetWhenMatches(t, currentTag, remoteTagPatternMap, req.Config.Matchers.Branches) {
				if req.Verbose {
					fmt.Fprintf(os.Stderr, "skip sync: %s (when conditions not met)\n", t.ID)
				}
				continue
			}
			if req.ReadOnly {
				syncResults = append(syncResults, actionResult{Name: fmt.Sprintf("[read-only] %s: would project release %s", t.ID, tag), OK: true})
			} else {
				syncResults = append(syncResults, projectRelease(ctx, t, req, tag, name, notes, allAssets)...)
			}
		}

		// Path 2: Mirror-driven default projection.
		// Mirrors with sync.releases that don't have an explicit override.
		resolvedMirrors, _ := config.ResolveAllMirrors(req.Config.Repos, req.Config.Forges, req.Config.Vars)
		for _, m := range resolvedMirrors {
			if !m.Sync.Releases || overriddenMirrors[m.ID] {
				continue
			}
			if req.ReadOnly {
				syncResults = append(syncResults, actionResult{Name: fmt.Sprintf("[read-only] mirror:%s: would project canonical release %s", m.ID, tag), OK: true})
			} else {
				syncResults = append(syncResults, projectToMirror(ctx, *m, tag, name, notes, req.Ref, req.Draft, req.Prerelease)...)
			}
		}

		if len(syncResults) > 0 {
			syncElapsed := time.Since(syncStart)
			output.SectionStart(w, "sf_sync", "Release Projection")
			syncSec := output.NewSection(w, "Release Projection", syncElapsed, color)
			for _, r := range syncResults {
				if r.OK {
					syncSec.Row("%s %s", output.StatusIcon("success", color), r.Name)
				} else {
					msg := "unknown error"
					if r.Err != nil {
						msg = r.Err.Error()
					}
					syncSec.Row("%s %s: %s", output.StatusIcon("failed", color), r.Name, msg)
				}
			}
			syncSec.Close()
			output.SectionEnd(w, "sf_sync")
		}
	}

	// ── Retention section (per active release target) ──
	// Each non-remote release target matching THIS event prunes its OWN releases:
	// a channel (Tag set) scopes candidates to its immutable dev-{sha} releases and
	// prunes the git tag too (pruneTags), protecting rolling aliases; a stable
	// target falls back to alias-pattern scope with tags kept. Per-target stores
	// never share a candidate set, so one target's retention can't touch another's.
	type retOutcome struct {
		id  string
		res *retention.Result
		err error
	}
	var retOutcomes []retOutcome
	retStart := time.Now()
	for _, t := range pipeline.CollectTargetsByKind(req.Config, "release") {
		if t.IsRemoteRelease() || t.Retention == nil || !t.Retention.Active() {
			continue
		}
		if !config.TargetMatchesEnv(t, req.Config) {
			continue
		}
		var patterns []string
		if t.Tag != "" {
			patterns = retention.TemplatesToPatterns([]string{t.Tag})
		} else if len(t.Aliases) > 0 {
			patterns = retention.TemplatesToPatterns(t.Aliases)
		}
		pol := *t.Retention
		// Rolling aliases are never pruned.
		pol.Protect = append(append([]string{}, pol.Protect...), retention.TemplatesToPatterns(t.Aliases)...)
		store := &forgeStore{forge: forgeClient, pruneTags: t.Tag != ""}
		res, err := retention.Apply(ctx, store, patterns, pol)
		retOutcomes = append(retOutcomes, retOutcome{id: t.ID, res: res, err: err})
	}
	if len(retOutcomes) > 0 {
		output.SectionStart(w, "sf_retention", "Retention")
		retSec := output.NewSection(w, "Retention", time.Since(retStart), color)
		for _, o := range retOutcomes {
			if o.err != nil {
				retSec.Row("%-16s%s", o.id, "error: "+o.err.Error())
				fmt.Fprintf(os.Stderr, "warning: release retention (%s): %v\n", o.id, o.err)
				continue
			}
			retSec.Row("%-16skept=%d pruned=%d", o.id, o.res.Kept, len(o.res.Deleted))
			for _, d := range o.res.Deleted {
				retSec.Row("  - %s", d)
			}
		}
		retSec.Close()
		output.SectionEnd(w, "sf_retention")
	}

	return nil
}

// buildImageRowsFromConfig builds image rows from config targets (fallback when no publish manifest).
func buildImageRowsFromConfig(cfg *config.Config, currentTag string, versionInfo *gitver.VersionInfo) []release.ImageRow {
	type imageKey struct{ host, path string }
	type pendingTarget struct {
		resolved registry.ResolvedRegistryTarget
		seen     map[string]bool
	}
	targetIndex := make(map[imageKey]*pendingTarget)
	var targetOrder []imageKey

	// CRITICAL:
	// tag_sources as map is ONLY for when.git_tags lookup on target conditions.
	// DO NOT reuse this for version selection — that would reintroduce
	// global filtering and break the search-path invariant.
	registryTagPatternMap := tagPatternLookupForConditionsOnly(cfg.Versioning.TagSources)

	for _, t := range pipeline.CollectTargetsByKind(cfg, "registry") {
		if !targetWhenMatches(t, currentTag, registryTagPatternMap, cfg.Matchers.Branches) {
			continue
		}
		resolved, resolveErr := config.ResolveRegistryForTarget(t, cfg.Registries, cfg.Vars)
		if resolveErr != nil {
			continue
		}
		regProvider := resolved.Provider
		if regProvider == "" {
			regProvider = build.DetectProvider(resolved.URL)
		}
		if p, err := registry.CanonicalProvider(regProvider); err == nil {
			regProvider = p
		} else {
			regProvider = "generic"
		}

		resolvedTags := gitver.ResolveTags(t.Tags, versionInfo)

		host := registry.NormalizeHost(resolved.URL)
		k := imageKey{host: host, path: resolved.Path}
		pt, exists := targetIndex[k]
		if !exists {
			pt = &pendingTarget{
				resolved: registry.ResolvedRegistryTarget{
					Provider: regProvider,
					Host:     host,
					Path:     resolved.Path,
				},
				seen: make(map[string]bool),
			}
			targetIndex[k] = pt
			targetOrder = append(targetOrder, k)
		}
		for _, rt := range resolvedTags {
			if !pt.seen[rt] {
				pt.seen[rt] = true
				pt.resolved.Tags = append(pt.resolved.Tags, rt)
			}
		}
	}

	sort.SliceStable(targetOrder, func(i, j int) bool {
		ri, rj := targetIndex[targetOrder[i]], targetIndex[targetOrder[j]]
		if ri.resolved.Provider != rj.resolved.Provider {
			return ri.resolved.Provider < rj.resolved.Provider
		}
		return ri.resolved.Host < rj.resolved.Host
	})

	imageRows := make([]release.ImageRow, 0, len(targetOrder))
	for _, k := range targetOrder {
		rt := targetIndex[k].resolved
		tags := make([]release.ResolvedTag, 0, len(rt.Tags))
		for _, t := range rt.Tags {
			tags = append(tags, release.ResolvedTag{
				Name: t,
				URL:  rt.TagURL(t),
			})
		}
		imageRows = append(imageRows, release.ImageRow{
			RegistryLabel: rt.DisplayName(),
			RegistryURL:   rt.RepoURL(),
			ImageRef:      rt.ImageRef(),
			Tags:          tags,
		})
	}

	return imageRows
}

// findPrimaryReleaseTarget returns the first release target with no remote forge fields (primary mode).
func findPrimaryReleaseTarget(cfg *config.Config) *config.TargetConfig {
	for _, t := range cfg.Targets {
		if t.Kind == "release" && !t.IsRemoteRelease() {
			return &t
		}
	}
	return nil
}

// activeReleaseTarget returns the first non-remote release target whose when:
// matches the current CI event — the channel being released on THIS build (dev on
// a push, stable on a tag). Falls back to the first non-remote release target if
// none matches (e.g. a manual run), preserving legacy single-target behavior.
func activeReleaseTarget(cfg *config.Config) *config.TargetConfig {
	var first *config.TargetConfig
	for i := range cfg.Targets {
		t := cfg.Targets[i]
		if t.Kind != "release" || t.IsRemoteRelease() {
			continue
		}
		if first == nil {
			first = &cfg.Targets[i]
		}
		if config.TargetMatchesEnv(t, cfg) {
			return &cfg.Targets[i]
		}
	}
	return first
}

// findRemoteReleaseTargets returns all release targets with remote forge fields set.
func findRemoteReleaseTargets(cfg *config.Config) []config.TargetConfig {
	var targets []config.TargetConfig
	for _, t := range cfg.Targets {
		if t.Kind == "release" && t.IsRemoteRelease() {
			targets = append(targets, t)
		}
	}
	return targets
}

// targetWhenMatches reports whether a release target's when: conditions match the
// current CI environment. It delegates to the canonical config.TargetMatches — it
// does NOT interpret when: itself — threading the release path's resolved tag.
func targetWhenMatches(t config.TargetConfig, currentTag string, tagPatterns map[string]string, branchPatterns map[string]string) bool {
	return config.TargetMatches(t, config.CIEvent(), config.CIBranch(), currentTag, tagPatterns, branchPatterns)
}

// renderCheckpoint renders a checkpoint line with pass/fail count, expanding failures.
func renderCheckpoint(sec *output.Section, color bool, label string, results []actionResult) {
	total := len(results)
	ok := 0
	var failed []actionResult
	for _, r := range results {
		if r.OK {
			ok++
		} else {
			failed = append(failed, r)
		}
	}

	status := "success"
	if ok != total {
		status = "failed"
	}
	icon := output.StatusIcon(status, color)

	sec.Row("%s %-7s %d/%d", icon, label+":", ok, total)

	for _, r := range failed {
		msg := "unknown error"
		if r.Err != nil {
			msg = r.Err.Error()
		}
		sec.Row("  - %s: %s", r.Name, msg)
	}
}

// hasActionFailures returns true if any result has a failure.
func hasActionFailures(results []actionResult) bool {
	for _, r := range results {
		if !r.OK {
			return true
		}
	}
	return false
}

// buildRegistryLinkFromTarget creates a forge release link for a registry target.
// Uses ResolvedRegistryTarget for deterministic URL generation.
func buildRegistryLinkFromTarget(url, path, tag, provider string) forge.ReleaseLink {
	rt := registry.ResolvedRegistryTarget{
		Provider: provider,
		Host:     registry.NormalizeHost(url),
		Path:     path,
	}
	return forge.ReleaseLink{
		Name:     fmt.Sprintf("%s %s", rt.DisplayName(), tag),
		URL:      rt.TagURL(tag),
		LinkType: "image",
	}
}

// ownerFromPath extracts the owner/org from "owner/repo" or "owner/repo/sub".
func ownerFromPath(path string) string {
	if idx := strings.IndexByte(path, '/'); idx >= 0 {
		return path[:idx]
	}
	return path
}

// repoFromPath extracts the repo name from "owner/repo".
func repoFromPath(path string) string {
	if idx := strings.IndexByte(path, '/'); idx >= 0 {
		rest := path[idx+1:]
		if idx2 := strings.IndexByte(rest, '/'); idx2 >= 0 {
			return rest[:idx2]
		}
		return rest
	}
	return path
}

// buildCatalogLink creates a GitLab Catalog release link for a component project.
func buildCatalogLink(remoteURL, tag string) forge.ReleaseLink {
	// Try CI env first (most reliable in GitLab CI).
	if serverURL := os.Getenv("CI_SERVER_URL"); serverURL != "" {
		if projectPath := os.Getenv("CI_PROJECT_PATH"); projectPath != "" {
			return forge.ReleaseLink{
				Name:     fmt.Sprintf("GitLab Catalog %s", tag),
				URL:      fmt.Sprintf("%s/explore/catalog/%s", serverURL, projectPath),
				LinkType: "other",
			}
		}
	}

	// Fallback: extract from remote URL.
	projectPath := projectPathFromRemote(remoteURL)
	if projectPath == "" {
		return forge.ReleaseLink{}
	}

	baseURL := forge.BaseURL(remoteURL)
	return forge.ReleaseLink{
		Name:     fmt.Sprintf("GitLab Catalog %s", tag),
		URL:      fmt.Sprintf("%s/explore/catalog/%s", baseURL, projectPath),
		LinkType: "other",
	}
}

// projectPathFromRemote extracts the "org/repo" project path from a git remote URL.
// Handles SSH (git@host:org/repo.git) and HTTPS (https://host/org/repo.git).
func projectPathFromRemote(remoteURL string) string {
	url := remoteURL

	// SSH format: git@host:org/repo.git or git@host:port:org/repo.git
	if idx := strings.Index(url, ":"); idx >= 0 && !strings.HasPrefix(url, "http") {
		path := url[idx+1:]
		// Handle SSH with port: git@host:port/org/repo.git
		if slashIdx := strings.Index(path, "/"); slashIdx >= 0 {
			// Check if part before / is a port number
			possiblePort := path[:slashIdx]
			isPort := true
			for _, c := range possiblePort {
				if c < '0' || c > '9' {
					isPort = false
					break
				}
			}
			if isPort {
				path = path[slashIdx+1:]
			}
		}
		return strings.TrimSuffix(path, ".git")
	}

	// HTTPS format: https://host/org/repo.git
	for _, prefix := range []string{"https://", "http://"} {
		if strings.HasPrefix(url, prefix) {
			withoutScheme := strings.TrimPrefix(url, prefix)
			// Remove host
			if slashIdx := strings.Index(withoutScheme, "/"); slashIdx >= 0 {
				path := withoutScheme[slashIdx+1:]
				return strings.TrimSuffix(path, ".git")
			}
		}
	}

	return ""
}

// detectRemoteURL gets the git remote origin URL.
func detectRemoteURL(rootDir string) (string, error) {
	det, err := build.DetectRepo(rootDir)
	if err != nil {
		return "", err
	}
	if det.GitInfo != nil && det.GitInfo.Remote != "" {
		return det.GitInfo.Remote, nil
	}
	return "", fmt.Errorf("no git remote URL found")
}

// newForgeClient creates a forge client from the detected provider and remote URL.
func newForgeClient(provider forge.Provider, remoteURL string) (forge.Forge, error) {
	baseURL := forge.BaseURL(remoteURL)

	switch provider {
	case forge.GitLab:
		return forge.NewGitLab(baseURL), nil
	case forge.GitHub:
		return forge.NewGitHub(baseURL), nil
	case forge.Gitea:
		return forge.NewGitea(baseURL), nil
	case forge.Forgejo:
		return forge.NewForgejo(baseURL), nil
	case forge.AzureDevOps:
		return forge.NewAzureDevOps(baseURL), nil
	default:
		return nil, fmt.Errorf("unknown forge provider: %s", provider)
	}
}

// refreshRollingRelease (re)creates a release at a rolling alias (e.g. latest-dev)
// and re-attaches its assets, so the alias is a pullable release that always
// points at the newest build. The Forge has no UpdateRelease, so this is
// delete-then-create; DeleteRelease tolerates a missing release (first run).
// Asset upload failures warn but do not abort the refresh.
func refreshRollingRelease(ctx context.Context, fc forge.Forge, alias, ref, name, notes string, prerelease bool, assets []string) error {
	_ = fc.DeleteRelease(ctx, alias) // ignore not-found — this is a refresh
	rel, err := fc.CreateRelease(ctx, forge.ReleaseOptions{
		TagName:     alias,
		Ref:         ref,
		Name:        name,
		Description: notes,
		Prerelease:  prerelease,
	})
	if err != nil {
		return err
	}
	for _, assetPath := range assets {
		if provision.IsPrivateKeyPath(assetPath) {
			fmt.Fprintf(os.Stderr, "refusing to upload signing key material as an asset: %s\n", filepath.Base(assetPath))
			continue
		}
		if err := fc.UploadAsset(ctx, rel.ID, forge.Asset{Name: filepath.Base(assetPath), FilePath: assetPath}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: rolling asset %s → %s: %v\n", filepath.Base(assetPath), alias, err)
		}
	}
	return nil
}

// newSyncForgeClientFromTarget creates a forge client for a remote release target.
// projectToMirror projects a canonical release to a mirror destination.
// Mirrors are first-class sources, not synthetic targets. Forge identity
// comes directly from the mirror config.
func projectToMirror(ctx context.Context, m config.ResolvedRepo, tag, name, notes, ref string, draft, prerelease bool) []actionResult {
	var results []actionResult
	label := "mirror:" + m.ID

	client, err := forge.NewFromAccessory(m.Provider, m.BaseURL, m.Project, m.Credentials)
	if err != nil {
		results = append(results, actionResult{Name: label, Err: err})
		fmt.Fprintf(os.Stderr, "warning: mirror projection to %s: %v\n", m.ID, err)
		return results
	}

	rel, err := client.CreateRelease(ctx, forge.ReleaseOptions{
		TagName:     tag,
		Ref:         ref,
		Name:        name,
		Description: notes,
		Draft:       draft,
		Prerelease:  prerelease,
	})
	if err != nil {
		results = append(results, actionResult{Name: label, Err: err})
		fmt.Fprintf(os.Stderr, "warning: release projection to %s: %v\n", m.ID, err)
		return results
	}
	results = append(results, actionResult{Name: fmt.Sprintf("%s: %s", label, rel.URL), OK: true})
	return results
}

// projectRelease projects a canonical release to a single destination via target config.
// Used by explicit target overrides only.
func projectRelease(ctx context.Context, t config.TargetConfig, req ReleaseCreateRequest, tag, name, notes string, allAssets []string) []actionResult {
	var results []actionResult

	syncClient, err := newSyncForgeClientFromTarget(t, req.Config)
	if err != nil {
		results = append(results, actionResult{Name: t.ID, Err: err})
		fmt.Fprintf(os.Stderr, "warning: projection to %s: %v\n", t.ID, err)
		return results
	}

	if t.SyncRelease {
		syncRel, err := syncClient.CreateRelease(ctx, forge.ReleaseOptions{
			TagName:     tag,
			Ref:         req.Ref,
			Name:        name,
			Description: notes,
			Draft:       req.Draft,
			Prerelease:  req.Prerelease,
		})
		if err != nil {
			results = append(results, actionResult{Name: t.ID, Err: err})
			fmt.Fprintf(os.Stderr, "warning: release projection to %s: %v\n", t.ID, err)
			return results
		}
		results = append(results, actionResult{Name: fmt.Sprintf("%s: %s", t.ID, syncRel.URL), OK: true})

		if t.SyncAssets {
			for _, assetPath := range allAssets {
				assetName := filepath.Base(assetPath)
				if provision.IsPrivateKeyPath(assetPath) {
					fmt.Fprintf(os.Stderr, "refusing to project signing key material to %s: %s\n", t.ID, assetName)
					continue
				}
				if err := syncClient.UploadAsset(ctx, syncRel.ID, forge.Asset{
					Name:     assetName,
					FilePath: assetPath,
				}); err != nil {
					fmt.Fprintf(os.Stderr, "warning: asset projection %s to %s: %v\n", assetName, t.ID, err)
				}
			}
		}
	}

	return results
}

func newSyncForgeClientFromTarget(t config.TargetConfig, cfg *config.Config) (forge.Forge, error) {
	// Resolve mirror reference — forge identity comes from the repo graph.
	if t.Mirror != "" {
		repo := config.FindRepoByID(cfg.Repos, t.Mirror)
		if repo == nil {
			return nil, fmt.Errorf("release target %s: mirror %q not found in repos", t.ID, t.Mirror)
		}
		resolved, err := config.ResolveRepo(*repo, cfg.Forges, cfg.Vars)
		if err != nil {
			return nil, fmt.Errorf("release target %s: resolving mirror %q: %w", t.ID, t.Mirror, err)
		}
		return forge.NewFromAccessory(resolved.Provider, resolved.BaseURL, resolved.Project, resolved.Credentials)
	}

	return nil, fmt.Errorf("release target %s: mirror: is required for remote release targets", t.ID)
}

// buildVerification assembles the publish-phase trust disclosure from WHATEVER trust
// evidence the results manifest records — any signature or provenance attestation —
// NOT gated on a Tier-0 signature. Disclosure is EVIDENCE-driven: an oidc/kms/hardware
// -only release still discloses its tier, transparency, trust domain, provenance
// attestations, and a class-appropriate verify recipe. Only the pinnable ANCHOR (a
// published cosign.pub + fingerprint + continuity claim) is CONTINUITY-driven —
// populated solely when this release carries a Tier-0 signature and the state-dir
// identity loads (never advertise a stale anchor for a release it did not sign). Those
// are different predicates and are deliberately decoupled. Returns (nil, "") only when
// there is no trust evidence at all. The second return is the anchor pubkey path to
// attach as a release asset, or "" when there is no anchor.
func buildVerification(cfg config.SigningConfig, results *artifact.ResultsManifest, repoRoot string) (*release.Verification, string) {
	if results == nil {
		return nil, ""
	}
	sigs := collectSignatures(results)
	provAtts := provenanceAttestations(results)
	if len(sigs) == 0 && len(provAtts) == 0 {
		return nil, "" // nothing worth disclosing
	}

	v := &release.Verification{ProvenanceAttestations: provAtts}
	if len(sigs) > 0 {
		p := sigs[0].ev // primary: Tier-0 first (collectSignatures sorts it ahead), else first
		v.TierLabel = signatureTierLabel(p)
		v.TrustClass = p.TrustClass
		v.TrustDomain = p.TrustDomain
		v.SignerRef = p.SignerRef
		v.Transparency = p.Transparency
		v.NonExportable = p.NonExportable
		v.PhysicalPresence = p.PhysicalPresence
		v.AdditionalLayers = describeLayers(sigs) // unique signatures beyond the primary
		v.ChecksumSig = primaryChecksumSig(sigs)  // detached-checksum asset for the recipe
	}

	// Continuity-driven anchor — only when this release was Tier-0-signed AND the
	// persistent identity loads. Decoupled from the disclosure above.
	if hasTier0(sigs) && cfg.StateDir.Configured() {
		if stateDir, err := cfg.StateDir.Resolve(); err == nil {
			if err := provision.GuardStateDir(stateDir, repoRoot); err == nil {
				if id, err := provision.LoadIdentity(stateDir); err == nil && id != nil {
					v.Fingerprint = id.Fingerprint
					v.AnchorAsset = "cosign.pub"
					v.Continuity = true
					return v, id.PubPath(stateDir)
				}
			}
		}
	}
	return v, ""
}

// provenanceAttestations describes the build-provenance predicates attested onto
// published image digests, each with the tier that authorized it. Kept SEPARATE
// from additionalSignatureLayers: a provenance attestation is a statement about
// how the artifact was built, not a signature over its bytes — the Verification
// surface must not conflate the two. Deduped.
func provenanceAttestations(results *artifact.ResultsManifest) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range results.Results {
		for _, o := range r.Outcomes {
			pa := o.ProvenanceAttestation
			if pa == nil || pa.Status != artifact.OutcomeSuccess {
				continue
			}
			pt := pa.PredicateType
			if pt == "" {
				pt = "provenance"
			}
			cls := pa.TrustClass
			if cls == "" {
				cls = "signature"
			}
			desc := pt + " · " + cls
			if pa.TrustDomain != "" {
				desc += " (trust domain: " + pa.TrustDomain + ")"
			}
			if pa.Tier != "" {
				desc += " (" + tierLabel(pa.Tier) + ")"
			}
			if pa.PhysicalPresence {
				desc += ", human-authorized"
			}
			if pa.NonExportable {
				desc += ", non-exportable"
			}
			if pa.VerifiedDigest != "" {
				desc += " · " + pa.VerifiedDigest
			}
			if !seen[desc] {
				seen[desc] = true
				out = append(out, desc)
			}
		}
	}
	return out
}

// sigEvidence is one successful signature's trust evidence plus its disclosure asset
// (a detached-checksum filename for a blob signature, or the image digest ref).
type sigEvidence struct {
	ev     artifact.TrustEvidence
	asset  string
	isBlob bool
}

// collectSignatures gathers every successful signature (blob + image), Tier-0 sorted
// FIRST so the disclosure "primary" is the continuity anchor when present, otherwise a
// stable first signature. Provenance attestations are NOT signatures and are collected
// separately (provenanceAttestations).
func collectSignatures(results *artifact.ResultsManifest) []sigEvidence {
	var sigs []sigEvidence
	for _, r := range results.Results {
		for _, o := range r.Outcomes {
			switch {
			case o.BlobSignature != nil && o.BlobSignature.Status == artifact.OutcomeSuccess:
				sigs = append(sigs, sigEvidence{o.BlobSignature.TrustEvidence, filepath.Base(o.BlobSignature.SignaturePath), true})
			case o.Attestation != nil && o.Attestation.Status == artifact.OutcomeSuccess:
				sigs = append(sigs, sigEvidence{o.Attestation.TrustEvidence, o.Attestation.SignatureRef, false})
			}
		}
	}
	sort.SliceStable(sigs, func(i, j int) bool {
		return sigs[i].ev.Tier == provision.TierSoftware && sigs[j].ev.Tier != provision.TierSoftware
	})
	return sigs
}

// describeSignature renders one signature's disclosure line (class, tier, trust
// domain, presence, non-exportability, asset).
func describeSignature(ev artifact.TrustEvidence, asset string) string {
	cls := ev.TrustClass
	if cls == "" {
		cls = "signature"
	}
	desc := cls
	if ev.Tier != "" {
		desc += " (" + tierLabel(ev.Tier) + ")"
	}
	if ev.TrustDomain != "" {
		desc += " (trust domain: " + ev.TrustDomain + ")"
	}
	if ev.PhysicalPresence {
		desc += " (human-authorized)"
	}
	if ev.NonExportable {
		desc += ", non-exportable"
	}
	if asset != "" {
		desc += " · " + asset
	}
	return desc
}

// describeLayers returns the deduped disclosure lines for every signature BEYOND the
// primary (sigs[0]) — the primary drives the main table, so its line is dropped here.
func describeLayers(sigs []sigEvidence) []string {
	seen := map[string]bool{}
	var descs []string
	for _, s := range sigs {
		d := describeSignature(s.ev, s.asset)
		if !seen[d] {
			seen[d] = true
			descs = append(descs, d)
		}
	}
	if len(descs) <= 1 {
		return nil
	}
	return descs[1:]
}

// primaryChecksumSig returns the detached-checksum signature asset to cite in the
// verify recipe — the first blob signature (SHA256SUMS.sig), Tier-0 preferred.
func primaryChecksumSig(sigs []sigEvidence) string {
	for _, s := range sigs {
		if s.isBlob {
			return s.asset
		}
	}
	return ""
}

// hasTier0 reports whether any collected signature is the continuity-backed Tier-0
// identity — the gate for advertising the published anchor.
func hasTier0(sigs []sigEvidence) bool {
	for _, s := range sigs {
		if s.ev.Tier == provision.TierSoftware {
			return true
		}
	}
	return false
}

// signatureTierLabel labels the primary signature for the table: its assurance tier
// when recorded (e.g. Tier-0), else a class-based label so non-tiered signers
// (kms/oidc/hardware) still read meaningfully.
func signatureTierLabel(ev artifact.TrustEvidence) string {
	if ev.Tier != "" {
		return tierLabel(ev.Tier)
	}
	switch ev.TrustClass {
	case "oidc":
		return "keyless (OIDC identity)"
	case "kms":
		return "KMS / managed key"
	case "hardware":
		return "hardware (operator-held key)"
	case "key":
		return "key (operator-supplied)"
	}
	return "signed"
}

func tierLabel(tier string) string {
	if tier == provision.TierSoftware {
		return "Tier-0 (persistent software key)"
	}
	return tier
}

// releaseAsset carries a download row, its on-disk asset path, the typed
// ArtifactID, and the artifact kind. Bundling identity with the row at
// construction time means the cross-kind canonicalization sort operates
// on the typed identity directly — no Name→ArtifactID reverse lookup is
// ever required.
type releaseAsset struct {
	Kind       string // "archive" | "binary"
	ArtifactID artifact.ArtifactID
	AssetPath  string
	Row        release.BinaryRow
}

// archivePlatform returns the platform string for an archive's release row.
//
// Single-source archive: use that source binary's OS/Arch (joined by
// exact ArtifactID match — no name reconstruction).
// Multi-source homogeneous archive: that single platform.
// Multi-source heterogeneous archive: "multi-platform". The earlier
// "first-by-ArtifactID" rule was structurally deterministic but
// semantically misleading; surfacing multi-platform reality directly is
// more accurate.
// No sources resolvable: empty string.
func archivePlatform(sources []artifact.ArtifactID, binaryByID map[artifact.ArtifactID]artifact.BinaryExecutionView) string {
	resolved := make([]string, 0, len(sources))
	for _, sourceID := range sources {
		if bv, ok := binaryByID[sourceID]; ok {
			resolved = append(resolved, bv.OS+"/"+bv.Arch)
		}
	}
	switch len(resolved) {
	case 0:
		return ""
	case 1:
		return resolved[0]
	default:
		first := resolved[0]
		for _, p := range resolved[1:] {
			if p != first {
				return "multi-platform"
			}
		}
		return first
	}
}

// credentialRefForHost walks the config's registries to find the credentials
// env-var prefix for a given host. Returns "" if no matching registry is
// configured — anonymous auth (which works for public registries).
//
// Config-driven, not derived from the artifact truth model. CredentialRef
// stays out of PublicationView per the Phase 4 step 1 lock-in: credentials
// are deployment configuration, not artifact identity.
//
// Host comparison normalizes registry URLs (strips scheme/path, lowercases)
// so a config entry of "https://ghcr.io/" matches a published host of
// "ghcr.io". Registries with ports (e.g. "registry.local:5000") match when
// the config entry is specified with the same port.
func credentialRefForHost(cfg *config.Config, host string) string {
	if cfg == nil {
		return ""
	}
	want := normalizeRegistryHost(host)
	for _, reg := range cfg.Registries {
		if normalizeRegistryHost(reg.URL) == want {
			return reg.Credentials
		}
	}
	return ""
}

// normalizeRegistryHost canonicalizes a host or URL to a comparable host
// string: lowercased, scheme/path stripped.
func normalizeRegistryHost(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(s)
}

// providerFromHost is a presentation-time classification of a registry host.
// Per the v2 contract, Provider is NOT stored in PublicationView or anywhere in
// the truth model — it is derived at consumer time for release-notes display.
//
// It MUST return a CANONICAL provider token from the same set
// ResolvedRegistryTarget.Provider uses (see registry/urls.go), because its
// result feeds rt.DisplayName / rt.RepoURL / rt.TagURL — and RepoURL/TagURL
// PANIC on an unrecognized provider. The `generic` fallback guarantees every
// returned token is one those builders handle. (A host the heuristic can't
// classify therefore renders as a neutral generic link, never a crash — e.g.
// a Harbor host whose name doesn't embed "harbor".)
//
// IMPORTANT: routing/auth decisions read Provider from RegistryConfig /
// ResolvedRegistry — the config-resolved authoritative source — never from this
// heuristic. For *accurate* per-host providers in release notes (vs. the neutral
// fallback), redirect to the config-resolved Provider rather than extending this.
//
// Substring matches for self-hosted vendor variants (gitea, harbor, jfrog)
// are intentional: they classify hosts whose names embed the vendor name
// (e.g. "harbor.example.com", "gitea.internal"), which is the common
// homelab/private-registry pattern. If a host happens to embed the
// substring without actually being that vendor, the worst case is a
// neutral mis-labeled display in release notes — never a routing change.
func providerFromHost(host string) string {
	h := strings.ToLower(host)
	switch {
	case h == "docker.io" || strings.HasSuffix(h, ".docker.io"):
		return "docker"
	case h == "ghcr.io" || strings.HasSuffix(h, ".ghcr.io"):
		return "github"
	case strings.HasSuffix(h, ".gitlab.io") || strings.HasPrefix(h, "registry.gitlab") || strings.Contains(h, "gitlab"):
		return "gitlab"
	case strings.HasSuffix(h, ".gitea.io") || strings.Contains(h, "gitea"):
		return "gitea"
	case strings.HasSuffix(h, ".harbor") || strings.Contains(h, "harbor"):
		return "harbor"
	case strings.HasSuffix(h, ".jfrog.io") || strings.Contains(h, "jfrog"):
		return "jfrog"
	case h == "quay.io" || strings.HasSuffix(h, ".quay.io"):
		return "quay"
	}
	return "generic"
}
