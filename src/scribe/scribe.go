// Package scribe is the scribe engine: it resolves a config's scribe.content /
// scribe.files into rendered modules and injects them into the workspace's marked
// regions. It is domain logic — it returns structured results and never renders
// terminal output (the CLI owns presentation). This is the seam the planned phase
// split wires: scribe runs in perform, the commit ("flush") at the end of publish.
package scribe

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/component"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitver"
	"github.com/PrPlanIT/StageFreight/src/manifest"
	"github.com/PrPlanIT/StageFreight/src/props"
	"github.com/PrPlanIT/StageFreight/src/registry"
	"github.com/PrPlanIT/StageFreight/src/scribe/render"
	"github.com/PrPlanIT/StageFreight/src/stencil"
)

// FileResult is the outcome for one scribe files: region. Data only — the CLI renders it.
type FileResult struct {
	File    string
	Status  string // "success" | "skipped"
	Detail  string // "updated" | "would update" | "unchanged"
	Preview string // dry-run: the would-be file content ("" when unchanged or writing)
}

// Preview is a dry-run standalone write (a build-contents output_file), returned as
// data so the CLI — never the engine — decides how to present it.
type Preview struct {
	Path    string
	Content string
}

// Result is the structured output of a scribe run.
type Result struct {
	Files    []FileResult
	Previews []Preview
}

// Run renders every scribe files: region from config, returning structured results.
// It performs the workspace mutations (or, in dry-run, computes previews) but never
// renders terminal output — that is the CLI's job.
func Run(appCfg *config.Config, rootDir string, dryRun, verbose bool) (*Result, error) {
	return runFiles(appCfg, rootDir, dryRun, verbose, func(config.FileDef) bool { return true })
}

// RunItems renders only the scribe files: regions that reference at least one of the
// given content item ids. It is the perform-time seam: a build-fed item (e.g. a
// generated-doc include) composes BEFORE the build that bakes it, while late items
// (badges needing registry/status) stay untouched for the publish pass. A no-match
// is a clean no-op (empty result), not an error.
func RunItems(appCfg *config.Config, rootDir string, itemIDs []string, dryRun, verbose bool) (*Result, error) {
	want := make(map[string]bool, len(itemIDs))
	for _, id := range itemIDs {
		want[id] = true
	}
	return runFiles(appCfg, rootDir, dryRun, verbose, func(f config.FileDef) bool {
		for _, ref := range RegionElementIDs(f) {
			if want[ref] {
				return true
			}
		}
		return false
	})
}

// runFiles is the shared engine core: it renders the scribe files: regions the `keep`
// predicate admits. Run passes keep-all; RunItems passes an item-scoped filter.
func runFiles(appCfg *config.Config, rootDir string, dryRun, verbose bool, keep func(config.FileDef) bool) (*Result, error) {
	if len(appCfg.Scribe.Files) == 0 {
		return nil, fmt.Errorf("no scribe files configured")
	}

	content := appCfg.StencilsByID()

	// Detect version for template resolution.
	versionInfo, err := build.DetectVersion(rootDir, appCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  warning: version detection failed: %v\n", err)
	}

	// Resolve publish-origin for badge image URLs — required only if a referenced
	// content is a badge (matches the old badge_ref hard-fail).
	publishBase, poErr := config.ResolvePublishOrigin(appCfg)
	if poErr != nil {
		if referencesBadge(appCfg.Scribe, content) {
			return nil, fmt.Errorf("scribe badge resolution: %w", poErr)
		}
		publishBase = ""
	}

	// Derive link base (blob URLs) from the same publish-origin repo.
	linkBase, _ := config.ResolveLinkBase(appCfg)

	res := &Result{}
	for _, file := range appCfg.Scribe.Files {
		if !keep(file) {
			continue
		}
		fr, previews, err := processFile(appCfg, file, content, rootDir, versionInfo, linkBase, publishBase, verbose, dryRun)
		if err != nil {
			return nil, err
		}
		res.Files = append(res.Files, fr)
		res.Previews = append(res.Previews, previews...)
	}
	return res, nil
}

// RenderContent resolves a single stencil (by id) to its markdown fragment. It is the
// ad-hoc, read-only counterpart to Run: it renders one declared stencil and returns the
// markdown; it never writes artifacts or mutates files. Only stencils declared in config
// are renderable — the config is the source of truth.
func RenderContent(appCfg *config.Config, rootDir, contentID string) (string, error) {
	content := appCfg.StencilsByID()
	def, ok := content[contentID]
	if !ok {
		return "", fmt.Errorf("no stencil %q", contentID)
	}

	versionInfo, err := build.DetectVersion(rootDir, appCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  warning: version detection failed: %v\n", err)
	}

	publishBase, poErr := config.ResolvePublishOrigin(appCfg)
	if poErr != nil {
		if def.EffectiveKind() == "badge" {
			return "", fmt.Errorf("scribe badge resolution: %w", poErr)
		}
		publishBase = ""
	}
	linkBase, _ := config.ResolveLinkBase(appCfg)

	return resolveStencilMarkdown(appCfg, def, linkBase, publishBase, versionInfo, rootDir)
}

// referencesBadge reports whether any files: region references a badge stencil —
// badges need the publish-origin base for their image URL.
func referencesBadge(s config.ScribeConfig, content map[string]config.StencilDef) bool {
	for _, f := range s.Files {
		for _, ref := range RegionElementIDs(f) {
			if def, ok := content[ref]; ok && def.EffectiveKind() == "badge" {
				return true
			}
		}
	}
	return false
}

// processFile renders one files: region (a marked span + its content refs). It returns
// the region's FileResult plus any dry-run previews for standalone output_file writes.
func processFile(appCfg *config.Config, file config.FileDef, content map[string]config.StencilDef, rootDir string, vi *gitver.VersionInfo, linkBase, rawBase string, verbose, dryRun bool) (FileResult, []Preview, error) {
	path := file.File
	if !filepath.IsAbs(path) {
		path = filepath.Join(rootDir, path)
	}

	// Read existing file (or start empty).
	fileContent := ""
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return FileResult{}, nil, fmt.Errorf("scribe: reading %s: %w", file.File, err)
		}
	} else {
		fileContent = string(raw)
	}
	original := fileContent

	var previews []Preview

	// build-contents refs with output_file write standalone files (independent of
	// the region embedding).
	for _, ref := range RegionElementIDs(file) {
		def, ok := content[ref]
		if !ok || def.EffectiveKind() != "build-contents" || def.OutputFile == "" {
			continue
		}
		m := resolveBuildContentsManifest(appCfg, def, rootDir)
		if m == nil {
			continue
		}
		rendered, rErr := manifest.RenderSection(m, def.Section, def.Render, def.Columns)
		if rErr != nil {
			fmt.Fprintf(os.Stderr, "scribe: build-contents output_file render: %v\n", rErr)
			continue
		}
		outPath := def.OutputFile
		if !filepath.IsAbs(outPath) {
			outPath = filepath.Join(rootDir, outPath)
		}
		if dryRun {
			previews = append(previews, Preview{Path: def.OutputFile, Content: rendered})
			continue
		}
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return FileResult{}, nil, fmt.Errorf("scribe: creating directory for %s: %w", def.OutputFile, err)
		}
		if err := os.WriteFile(outPath, []byte(rendered+"\n"), 0o644); err != nil {
			return FileResult{}, nil, fmt.Errorf("scribe: writing %s: %w", def.OutputFile, err)
		}
		if verbose {
			fmt.Fprintf(os.Stderr, "  wrote %s\n", def.OutputFile)
		}
	}

	// Render the region content (items sugar or a freeform body) and place it.
	composed, rErr := renderRegion(appCfg, file, content, linkBase, rawBase, vi, rootDir)
	if rErr != nil {
		return FileResult{}, nil, fmt.Errorf("scribe: %w", rErr)
	}
	if composed != "" && file.Between[0] != "" && file.Between[1] != "" {
		updated, found := registry.ReplaceBetween(fileContent, file.Between[0], file.Between[1], composed)
		if found {
			fileContent = updated
		} else if verbose {
			fmt.Fprintf(os.Stderr, "  warning: markers not found in %s: %s ... %s\n", file.File, file.Between[0], file.Between[1])
		}
	}

	if dryRun {
		if fileContent != original {
			return FileResult{File: file.File, Status: "success", Detail: "would update", Preview: fileContent}, previews, nil
		}
		return FileResult{File: file.File, Status: "skipped", Detail: "unchanged"}, previews, nil
	}
	if fileContent == original {
		return FileResult{File: file.File, Status: "skipped", Detail: "unchanged"}, previews, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return FileResult{}, nil, fmt.Errorf("scribe: creating directory for %s: %w", file.File, err)
	}
	if err := os.WriteFile(path, []byte(fileContent), 0o644); err != nil {
		return FileResult{}, nil, fmt.Errorf("scribe: writing %s: %w", file.File, err)
	}
	return FileResult{File: file.File, Status: "success", Detail: "updated"}, previews, nil
}

// resolveStencilMarkdown resolves ONE stencil to its markdown fragment, dispatching on
// EffectiveKind to the matching render module (the module set survives as element
// producers — only their composition changed). A hard error (a badge with no resolvable
// image URL) is returned; a soft skip (a producer that can't be served, a missing
// include) yields "" so the element simply contributes nothing — matching the old
// Compose skip-empty semantics.
func resolveStencilMarkdown(appCfg *config.Config, def config.StencilDef, linkBase, rawBase string, vi *gitver.VersionInfo, rootDir string) (string, error) {
	switch def.EffectiveKind() {
	case "badge":
		if def.Output == "" {
			return "", fmt.Errorf("stencils.%s: badge has no output path", def.ID)
		}
		link := gitver.ResolveVars(def.Link, appCfg.Vars)
		if vi != nil {
			link = gitver.ResolveTemplateWithDirAndVars(link, vi, rootDir, appCfg.Vars)
		}
		resolved := def
		resolved.Link = link
		mod := resolveBadgeModule(resolved, linkBase, rawBase)
		if mod == nil {
			return "", fmt.Errorf("stencils.%s: badge has no resolvable image URL (publish-origin unresolved)", def.ID)
		}
		return mod.Render(), nil

	case "shield":
		// Two forms: a raw shields.io path (escape hatch, `shield:`) or an inline
		// composition from verbs (label/message/color/logo/logo_color) — SF builds the
		// shields.io URL so configs stop hand-escaping %2F.
		var shieldPath string
		if def.Shield != "" {
			shieldPath = gitver.ResolveVarsShields(def.Shield, appCfg.Vars)
		} else {
			shieldPath = composeShieldPath(def, appCfg.Vars)
		}
		link := gitver.ResolveVars(def.Link, appCfg.Vars)
		label := def.LabelOrID()
		if def.Label == "" && def.Shield != "" {
			label = shieldPath // raw-path form: fall back to the path as alt
		}
		if vi != nil {
			label = gitver.ResolveTemplateWithDirAndVars(label, vi, "", appCfg.Vars)
		}
		return render.ShieldModule{
			Path:  shieldPath,
			Label: label,
			Link:  resolveLink(link, linkBase),
		}.Render(), nil

	case "text":
		text := def.Content
		if vi != nil {
			text = gitver.ResolveTemplateWithDirAndVars(text, vi, "", appCfg.Vars)
		}
		return render.TextModule{Text: text}.Render(), nil

	case "component":
		spec, err := component.ParseSpec(def.Spec)
		if err != nil {
			fmt.Fprintf(os.Stderr, "scribe: component %s: %v\n", def.Spec, err)
			return "", nil
		}
		docs := component.GenerateDocs([]*component.SpecFile{spec})
		return render.ComponentModule{Docs: strings.TrimSpace(docs)}.Render(), nil

	case "include":
		incPath := def.Path
		if !filepath.IsAbs(incPath) {
			incPath = filepath.Join(rootDir, incPath)
		}
		data, err := os.ReadFile(incPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "scribe: include %s: %v\n", def.Path, err)
			return "", nil
		}
		return render.IncludeModule{Content: strings.TrimSpace(string(data))}.Render(), nil

	case "build-contents":
		m := resolveBuildContentsManifest(appCfg, def, rootDir)
		if m == nil {
			return "", nil
		}
		return render.BuildContentsModule{
			Manifest: m,
			Section:  def.Section,
			Renderer: def.Render,
			Columns:  def.Columns,
			Wrap:     def.Wrap,
			Summary:  def.Summary,
		}.Render(), nil

	case "k8s-inventory":
		sha := ""
		if vi != nil {
			sha = vi.SHA
		}
		return (&render.K8sInventoryModule{
			CatalogPath:   def.CatalogPath,
			CommitSHA:     sha,
			RepoRoot:      rootDir,
			ClusterConfig: appCfg.GitOps.Cluster,
		}).Render(), nil

	case "props":
		pdef, ok := props.Get(def.Type)
		if !ok {
			fmt.Fprintf(os.Stderr, "scribe: props type %q not found\n", def.Type)
			return "", nil
		}
		// Derive coordinates from repos: (publish-origin, or a repo: override) so
		// producers are self-contained; explicit params override the derived ones.
		resolvedParams, skipReason := resolveProducerParams(appCfg, def, pdef)
		if skipReason != "" {
			fmt.Fprintf(os.Stderr, "scribe: props %s skipped: %s\n", def.Type, skipReason)
			return "", nil
		}
		for k, v := range def.Params {
			resolvedParams[k] = gitver.ResolveVars(v, appCfg.Vars)
		}
		opts := props.RenderOptions{
			Label:   def.Label,
			Link:    gitver.ResolveVars(def.Link, appCfg.Vars),
			Style:   def.Style,
			Logo:    def.Logo,
			Variant: props.VariantClassic,
		}
		resolved, err := props.ResolveDefinition(pdef, resolvedParams, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "scribe: props %s: %v\n", def.Type, err)
			return "", nil
		}
		return render.PropsModule{
			Resolved: resolved,
			Variant:  props.VariantClassic,
		}.Render(), nil
	}
	return "", nil
}

// renderRegion produces a files region's content string. A freeform body: is rendered
// through the stencil grammar ({id} embeds + {#if}) plus a gitver leaf-pass; the items:
// sugar resolves each stencil and joins them — space within a row, a blank line between
// rows on "br", empty elements skipped — reproducing the old Compose/ComposeInline
// semantics exactly (no extra gitver pass: element output is token-terminal).
func renderRegion(appCfg *config.Config, file config.FileDef, content map[string]config.StencilDef, linkBase, rawBase string, vi *gitver.VersionInfo, rootDir string) (string, error) {
	if strings.TrimSpace(file.Body) != "" {
		var firstErr error
		resolve := func(id string) (string, bool) {
			def, ok := content[id]
			if !ok {
				return "", false // not a stencil → leave for the gitver leaf-pass
			}
			md, err := resolveStencilMarkdown(appCfg, def, linkBase, rawBase, vi, rootDir)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return "", true
			}
			return md, true
		}
		out := renderText(file.Body, stencil.Env{Resolve: resolve}, vi, rootDir, appCfg.Vars)
		return out, firstErr
	}

	resolveOne := func(ref string) (string, error) {
		def, ok := content[ref]
		if !ok {
			return "", fmt.Errorf("files.%s: item %q not found in stencils", file.ID, ref)
		}
		return resolveStencilMarkdown(appCfg, def, linkBase, rawBase, vi, rootDir)
	}

	if file.Inline {
		var parts []string
		for _, ref := range file.Items {
			if ref == "br" {
				continue
			}
			md, err := resolveOne(ref)
			if err != nil {
				return "", err
			}
			if md != "" {
				parts = append(parts, md)
			}
		}
		return strings.Join(parts, " "), nil
	}

	var rows []string
	var current []string
	flush := func() {
		if len(current) > 0 {
			rows = append(rows, strings.Join(current, " "))
			current = nil
		}
	}
	for _, ref := range file.Items {
		if ref == "br" {
			flush()
			continue
		}
		md, err := resolveOne(ref)
		if err != nil {
			return "", err
		}
		if md != "" {
			current = append(current, md)
		}
	}
	flush()
	return strings.Join(rows, "\n\n"), nil
}

// RegionElementIDs returns the stencil ids a region references — the body's {id} embeds
// for a freeform body, or the item refs (minus "br") for the items sugar.
func RegionElementIDs(f config.FileDef) []string {
	if strings.TrimSpace(f.Body) != "" {
		return referencedElementIDs(f.Body)
	}
	var ids []string
	for _, ref := range f.Items {
		if ref != "br" {
			ids = append(ids, ref)
		}
	}
	return ids
}

// referencedElementIDs scans a body for {name} embeds, skipping {{escaped}} literals and
// {#if}/{/if} control tokens. Order-preserving, de-duplicated.
func referencedElementIDs(body string) []string {
	var ids []string
	seen := map[string]bool{}
	s := body
	for {
		i := strings.IndexByte(s, '{')
		if i < 0 {
			break
		}
		s = s[i:]
		if strings.HasPrefix(s, "{{") {
			s = s[2:]
			continue
		}
		j := strings.IndexByte(s, '}')
		if j < 0 {
			break
		}
		name := strings.TrimSpace(s[1:j])
		if name != "" && !strings.HasPrefix(name, "#") && !strings.HasPrefix(name, "/") && !seen[name] {
			seen[name] = true
			ids = append(ids, name)
		}
		s = s[j+1:]
	}
	return ids
}

// composeShieldPath builds a shields.io static-badge path ("badge/<label>-<message>-<color>")
// from a content def's inline verbs, applying shields.io escaping (_→__, -→--, space→_) and
// URL path-encoding (/ → %2F) so configs never hand-escape. logo/logo_color become query params.
func composeShieldPath(def config.StencilDef, vars map[string]string) string {
	label := gitver.ResolveVars(def.Label, vars)
	message := gitver.ResolveVars(def.Message, vars)
	color := strings.TrimPrefix(gitver.ResolveVars(def.Color, vars), "#")

	parts := make([]string, 0, 3)
	if label != "" {
		parts = append(parts, escapeShieldSegment(label))
	}
	parts = append(parts, escapeShieldSegment(message))
	if color != "" {
		parts = append(parts, color)
	}
	path := "badge/" + strings.Join(parts, "-")

	q := url.Values{}
	if logo := gitver.ResolveVars(def.Logo, vars); logo != "" {
		q.Set("logo", logo)
	}
	if def.LogoColor != "" {
		q.Set("logoColor", def.LogoColor)
	}
	if def.LabelColor != "" {
		q.Set("labelColor", strings.TrimPrefix(def.LabelColor, "#"))
	}
	if def.Style != "" {
		q.Set("style", def.Style)
	}
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	return path
}

// escapeShieldSegment applies shields.io static-badge escaping then URL path-encoding.
func escapeShieldSegment(s string) string {
	s = strings.ReplaceAll(s, "_", "__")
	s = strings.ReplaceAll(s, "-", "--")
	s = strings.ReplaceAll(s, " ", "_")
	return url.PathEscape(s)
}

// resolveProducerParams derives the coordinate params (module|repo) a named producer
// needs from repos: context — publish-origin by default, or def.Repo override — so
// producers stop restating {var:...} coordinates. Returns a non-empty skip reason
// (never a dead <img>) when the resolved host can't serve the producer.
func resolveProducerParams(appCfg *config.Config, def config.StencilDef, pdef props.Definition) (map[string]string, string) {
	params := map[string]string{}

	// Which coordinate does this producer consume?
	var needsModule, needsRepo bool
	for _, p := range pdef.Resolver.Schema().Params {
		switch p.Name {
		case "module":
			needsModule = true
		case "repo":
			needsRepo = true
		}
	}
	if !needsModule && !needsRepo {
		return params, "" // producer is self-contained (static/no coordinate)
	}

	coords, err := config.ResolveProducerCoords(appCfg, def.Repo)
	if err != nil {
		return nil, fmt.Sprintf("cannot resolve producer coordinates: %v", err)
	}
	if ok, need := producerHostOK(def.Type, coords.Host); !ok {
		return nil, fmt.Sprintf("host %q cannot serve %q (needs %s)", coords.Host, def.Type, need)
	}
	if needsModule {
		params["module"] = coords.Module
	}
	if needsRepo {
		params["repo"] = coords.Project
	}
	return params, ""
}

// producerHostOK reports whether a producer can be served for the resolved host,
// returning the required host class when it can't. goreportcard / pkg.go.dev index
// only public Go hosts; shields.io github/* and gitlab/* endpoints are host-specific.
func producerHostOK(producerID, host string) (bool, string) {
	switch {
	case producerID == "goreportcard" || producerID == "go-reference":
		switch host {
		case "github.com", "gitlab.com", "bitbucket.org":
			return true, ""
		}
		return false, "github.com/gitlab.com/bitbucket.org"
	case strings.HasPrefix(producerID, "github-"):
		return host == "github.com", "github.com"
	case strings.HasPrefix(producerID, "gitlab-"):
		return strings.Contains(host, "gitlab"), "a gitlab host"
	}
	return true, "" // unknown producer: impose no host constraint
}

// resolveBadgeModule builds a BadgeModule from a badge content def, using its SVG
// output path + the raw-content base to form the image URL.
func resolveBadgeModule(def config.StencilDef, linkBase, rawBase string) render.Module {
	var imgURL string
	if def.Output != "" && rawBase != "" {
		imgURL = rawBase + "/" + strings.TrimPrefix(def.Output, "./")
	}
	if imgURL == "" {
		return nil
	}
	return render.BadgeModule{
		Alt:    def.LabelOrID(),
		ImgURL: imgURL,
		Link:   resolveLink(def.Link, linkBase),
	}
}

// resolveBuildContentsManifest loads or generates a manifest for a build-contents def.
func resolveBuildContentsManifest(appCfg *config.Config, def config.StencilDef, rootDir string) *manifest.Manifest {
	if def.Source != "" {
		srcPath := def.Source
		if !filepath.IsAbs(srcPath) {
			srcPath = filepath.Join(rootDir, srcPath)
		}
		m, err := manifest.LoadManifest(srcPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "scribe: build-contents source %s: %v\n", def.Source, err)
			return nil
		}
		return m
	}

	// Determine which build owns this inventory. Ownership is declared, never
	// inferred from build-list position.
	buildID := def.Build
	if buildID == "" {
		switch len(appCfg.Builds) {
		case 0:
			fmt.Fprintf(os.Stderr, "scribe: build-contents: no builds configured\n")
			return nil
		case 1:
			buildID = appCfg.Builds[0].ID
		default:
			fmt.Fprintf(os.Stderr, "scribe: build-contents: multiple builds configured — set build: to the owning build id\n")
			return nil
		}
	}

	manifestPath := manifest.ResolveManifestPath(rootDir, appCfg, buildID)
	if m, err := manifest.LoadManifest(manifestPath); err == nil {
		return m
	}

	manifests, err := manifest.Generate(appCfg, manifest.GenerateOptions{
		RootDir: rootDir,
		BuildID: buildID,
		Mode:    "ephemeral",
		DryRun:  true,
	})
	if err != nil || len(manifests) == 0 {
		fmt.Fprintf(os.Stderr, "scribe: build-contents manifest generation failed: %v\n", err)
		return nil
	}
	return manifests[0]
}

// resolveLink resolves a relative link against a base URL.
func resolveLink(link, linkBase string) string {
	if link == "" {
		return ""
	}
	if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") || strings.HasPrefix(link, "/") {
		return link
	}
	if linkBase != "" {
		return linkBase + "/" + strings.TrimPrefix(link, "./")
	}
	return link
}
