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

	"github.com/spf13/cobra"
	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/credentials"
	"github.com/PrPlanIT/StageFreight/src/diag"
	"github.com/PrPlanIT/StageFreight/src/forge"
	"github.com/PrPlanIT/StageFreight/src/gitver"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/registry"
	"github.com/PrPlanIT/StageFreight/src/release"
	"github.com/PrPlanIT/StageFreight/src/retention"
)

// ReleaseCreateRequest is the explicit input contract for RunReleaseCreate.
// Cobra command fills this from flags; CI runner fills it from config/ciCtx.
// Ctx is inside the request (matches docker.Request pattern).
type ReleaseCreateRequest struct {
	Ctx             context.Context
	RootDir         string
	Config          *config.Config
	Tag             string
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

		for _, av := range archiveViews {
			if av.BuildStatus != artifact.OutcomeSuccess {
				continue
			}
			for _, sourceID := range av.Sources {
				coveredIDs[sourceID] = struct{}{}
			}
			assets = append(assets, releaseAsset{
				Kind:       "archive",
				ArtifactID: av.ArtifactID,
				AssetPath:  av.Path,
				Row: release.BinaryRow{
					Name:     av.ArtifactName,
					Platform: archivePlatform(av, binaryByID),
					Size:     av.Size,
					SHA256:   av.SHA256,
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

		input := release.NotesInput{
			RepoDir:      rootDir,
			ToRef:        tag,
			TagPatterns:  tagPatterns,
			SecurityTile: secTile,
			SecurityBody: secBody,
			Version:      versionInfo.Version,
			SHA:          sha,
			IsPrerelease: versionInfo.IsPrerelease,
			Images:       imageRows,
			Downloads:    downloadRows,
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

	// Collect release targets from config
	primaryRelease := findPrimaryReleaseTarget(req.Config)
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
				syncResults = append(syncResults, projectToMirror(ctx, *m, tag, name, notes, req.Draft, req.Prerelease)...)
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

	// ── Retention section (from primary release target) ──
	if primaryRelease != nil && primaryRelease.Retention != nil && primaryRelease.Retention.Active() {
		retStart := time.Now()
		var patterns []string
		if len(primaryRelease.Aliases) > 0 {
			patterns = retention.TemplatesToPatterns(primaryRelease.Aliases)
		}
		store := &forgeStore{forge: forgeClient}
		result, retErr := retention.Apply(ctx, store, patterns, *primaryRelease.Retention)

		retElapsed := time.Since(retStart)

		output.SectionStart(w, "sf_retention", "Retention")
		retSec := output.NewSection(w, "Retention", retElapsed, color)

		if retErr != nil {
			retSec.Row("error: %v", retErr)
			fmt.Fprintf(os.Stderr, "warning: release retention: %v\n", retErr)
		} else {
			retSec.Row("%-16s%d", "matched", result.Matched)
			retSec.Row("%-16s%d", "kept", result.Kept)
			retSec.Row("%-16s%d", "pruned", len(result.Deleted))
			for _, d := range result.Deleted {
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

// targetWhenMatches checks if a target's when conditions match the current CI environment.
// Resolves policy names from the provided policies config.
func targetWhenMatches(t config.TargetConfig, currentTag string, tagPatterns map[string]string, branchPatterns map[string]string) bool {
	if len(t.When.GitTags) > 0 && currentTag != "" {
		resolved := resolveWhenPatternsFromCfg(t.When.GitTags, tagPatterns)
		if !config.MatchPatterns(resolved, currentTag) {
			return false
		}
	}
	if len(t.When.Branches) > 0 {
		branch := resolveBranchFromEnv()
		resolved := resolveWhenPatternsFromCfg(t.When.Branches, branchPatterns)
		if !config.MatchPatterns(resolved, branch) {
			return false
		}
	}
	return true
}

// resolveWhenPatternsFromCfg resolves when condition entries to regex patterns.
// "re:" prefixed entries are inline regex, others are policy name lookups.
func resolveWhenPatternsFromCfg(entries []string, policyMap map[string]string) []string {
	resolved := make([]string, 0, len(entries))
	for _, entry := range entries {
		if len(entry) > 3 && entry[:3] == "re:" {
			resolved = append(resolved, entry[3:])
		} else if regex, ok := policyMap[entry]; ok {
			resolved = append(resolved, regex)
		} else {
			resolved = append(resolved, entry)
		}
	}
	return resolved
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

// resolveBranchFromEnv resolves the current branch from CI environment variables.
func resolveBranchFromEnv() string {
	if b := os.Getenv("CI_COMMIT_BRANCH"); b != "" {
		return b
	}
	if b := os.Getenv("GITHUB_REF_NAME"); b != "" {
		return b
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

// newSyncForgeClientFromTarget creates a forge client for a remote release target.
// projectToMirror projects a canonical release to a mirror destination.
// Mirrors are first-class sources, not synthetic targets. Forge identity
// comes directly from the mirror config.
func projectToMirror(ctx context.Context, m config.ResolvedRepo, tag, name, notes string, draft, prerelease bool) []actionResult {
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
func archivePlatform(av artifact.ArchiveExecutionView, binaryByID map[artifact.ArtifactID]artifact.BinaryExecutionView) string {
	resolved := make([]string, 0, len(av.Sources))
	for _, sourceID := range av.Sources {
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

