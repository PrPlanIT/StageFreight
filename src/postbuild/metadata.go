package postbuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/forge"
	"github.com/PrPlanIT/StageFreight/src/gitver"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/registry"
)

// Project-identity sync (kind: metadata → registry overviews + forge repo descriptions) is
// driven from the publish phase via metadataTargetsDue + RunMetadataSection. It is NOT a
// post-build hook: a former MetadataHook was never registered, so the sync silently died
// (e1f2189) until publish was wired to call it directly, guarded by TestMetadataTargetsDue.

type metaRow struct{ dest, detail string }

// RunMetadataSection pushes each kind: metadata target's identity to its registry: and
// repos: destinations, routing each field to what the destination supports and reporting
// set/skipped/warned honestly. Never truncates, never touches org scope.
func RunMetadataSection(ctx context.Context, w io.Writer, color bool, targets []config.TargetConfig, rootDir string, appCfg *config.Config) (string, time.Duration) {
	output.SectionStartCollapsed(w, "sf_metadata", "Project Metadata")
	start := time.Now()

	linkBase, _ := config.ResolveLinkBase(appCfg)
	rawBase, _ := config.ResolvePublishOrigin(appCfg)

	// Committed content-hash state → idempotent avatar sync (upload only on change). The
	// served avatar can be reprocessed, so hashing it back is unreliable; a persisted local
	// hash is the reliable gate (same "commit generated state" pattern as badge SVGs).
	state := loadMetadataState(rootDir)
	stateDirty := false

	var pushed, errs int
	var rows []metaRow

	for _, t := range targets {
		// Source each identity field from the target when it sets it, else fall back to the
		// first-class metadata: block (the settled single source). Resolve {var:} once.
		m := appCfg.Metadata
		descList := t.Description
		if len(descList) == 0 {
			descList = m.Description.Default
		}
		variants := make([]string, 0, len(descList))
		for _, d := range descList {
			variants = append(variants, gitver.ResolveVars(d, appCfg.Vars))
		}
		websiteSrc := t.Website
		if websiteSrc == "" {
			websiteSrc = m.Website
		}
		website := gitver.ResolveVars(websiteSrc, appCfg.Vars)
		topicsSrc := t.Topics
		if len(topicsSrc) == 0 {
			topicsSrc = m.Topics
		}
		topics, topicWarnings := normalizeTopics(topicsSrc)
		readme := t.Readme
		if readme == "" {
			readme = m.Readme.Default
		}
		logo := t.Logo
		if logo == "" {
			logo = m.Icon
		}

		// ── registry destinations: short description (cap-fit) + long readme body ──
		for _, rid := range t.Registry {
			reg, err := config.ResolveRegistryForTarget(
				config.TargetConfig{ID: t.ID, Kind: "metadata", Registry: config.StringOrList{rid}, Path: t.Path},
				appCfg.Registries, appCfg.Vars)
			if err != nil || reg == nil {
				errs++
				rows = append(rows, metaRow{rid, "unresolved registry"})
				continue
			}
			provider := reg.Provider
			if provider == "" {
				provider = build.DetectProvider(reg.URL)
			}

			var full, derivedShort string
			if readme != "" {
				if content, perr := registry.PrepareReadmeFromFile(readme, "", linkBase, rawBase, rootDir); perr == nil {
					full, derivedShort = content.Full, content.Short
				}
			}
			short, fitOK := fitDescription(variants, providerDescCap(provider))
			if len(variants) > 0 && !fitOK {
				rows = append(rows, metaRow{reg.URL + "/" + reg.Path, fmt.Sprintf("warn: no description variant ≤ %d chars", providerDescCap(provider))})
			}
			if short == "" {
				short = derivedShort // fall back to the readme's first paragraph
			}

			client, err := registry.NewRegistry(provider, reg.URL, reg.Credentials)
			if err != nil {
				errs++
				rows = append(rows, metaRow{rid, "client error: " + err.Error()})
				continue
			}
			if err := client.UpdateDescription(ctx, reg.Path, short, full); err != nil {
				errs++
				rows = append(rows, metaRow{reg.URL + "/" + reg.Path, "error: " + err.Error()})
				continue
			}
			pushed++
			rows = append(rows, metaRow{reg.URL + "/" + reg.Path, registrySetDetail(provider, short, full)})
		}

		// ── forge repo destinations: description / website / topics / logo per capability ──
		for _, repoID := range t.Repos {
			rc := config.FindRepoByID(appCfg.Repos, repoID)
			if rc == nil {
				errs++
				rows = append(rows, metaRow{repoID, "unknown repo"})
				continue
			}
			resolved, err := config.ResolveRepo(*rc, appCfg.Forges, appCfg.Vars)
			if err != nil {
				errs++
				rows = append(rows, metaRow{repoID, "resolve error: " + err.Error()})
				continue
			}
			fc, err := forge.NewFromAccessory(resolved.Provider, resolved.BaseURL, resolved.Project, resolved.Credentials)
			if err != nil {
				errs++
				rows = append(rows, metaRow{repoID, "client error: " + err.Error()})
				continue
			}
			ms, ok := fc.(forge.MetadataSetter)
			if !ok {
				rows = append(rows, metaRow{resolved.Project + "@" + resolved.Provider, "metadata not supported by " + resolved.Provider})
				continue
			}
			short, _ := fitDescription(variants, providerDescCap(resolved.Provider))

			// Idempotent logo: only pass LogoPath when the file's hash differs from what we
			// last uploaded to this destination.
			logoPath, logoHash, logoKey := "", "", "logo:"+repoID
			if logo != "" {
				abs := logo
				if !filepath.IsAbs(abs) {
					abs = filepath.Join(rootDir, logo)
				}
				if h, herr := hashFile(abs); herr == nil {
					logoHash = h
					if state[logoKey] != h {
						logoPath = abs // changed (or never uploaded) → upload
					}
				}
			}

			out, err := ms.UpdateRepoMetadata(ctx, forge.RepoMetadata{
				Description: short,
				Website:     website,
				Topics:      topics,
				LogoPath:    logoPath,
			})
			if err != nil {
				errs++
				rows = append(rows, metaRow{resolved.Project + "@" + resolved.Provider, "error: " + err.Error()})
				continue
			}
			pushed++
			// Record the uploaded logo hash so the next run is a no-op unless it changes.
			for _, f := range out.Set {
				if f == "logo" && logoHash != "" {
					state[logoKey] = logoHash
					stateDirty = true
				}
			}
			rows = append(rows, metaRow{resolved.Project + "@" + resolved.Provider, forgeSetDetail(out, topicWarnings)})
		}
	}

	if stateDirty {
		if err := saveMetadataState(rootDir, state); err != nil {
			rows = append(rows, metaRow{".stagefreight/metadata-state.json", "warn: could not persist idempotency state: " + err.Error()})
		}
	}

	elapsed := time.Since(start)
	sec := output.NewSection(w, "Metadata", elapsed, color)
	for _, r := range rows {
		sec.Row("%-44s%s", r.dest, r.detail)
	}
	sec.Close()
	output.SectionEnd(w, "sf_metadata")

	summary := fmt.Sprintf("%d pushed", pushed)
	if errs > 0 {
		summary += fmt.Sprintf(", %d error(s)", errs)
	}
	return summary, elapsed
}

// providerDescCap returns the short-description character cap for a provider (0 = no cap
// known → use the fullest variant). Docker Hub's ~100 is the tightest; GitHub ~350.
func providerDescCap(provider string) int {
	switch provider {
	case "docker", "dockerhub":
		return 100
	case "github":
		return 350
	default: // harbor, jfrog, quay, gitlab, gitea, forgejo — generous/unknown
		return 0
	}
}

// fitDescription picks the LONGEST variant whose length fits cap (cap 0 = the fullest).
// Returns ("", false) when nothing fits — the caller warns and skips that field.
func fitDescription(variants []string, cap int) (string, bool) {
	if len(variants) == 0 {
		return "", false
	}
	vs := append([]string(nil), variants...)
	sort.SliceStable(vs, func(i, j int) bool { return len([]rune(vs[i])) > len([]rune(vs[j])) })
	if cap <= 0 {
		return vs[0], true
	}
	for _, v := range vs {
		if len([]rune(v)) <= cap {
			return v, true
		}
	}
	return "", false
}

// normalizeTopics lowers authored topics to the strict portable form, collecting a warning
// per transform or drop.
func normalizeTopics(raw []string) ([]string, []string) {
	if len(raw) == 0 {
		return nil, nil
	}
	var out, warnings []string
	for _, t := range raw {
		n, changed := forge.NormalizeTopic(t)
		if n == "" {
			warnings = append(warnings, fmt.Sprintf("topic %q dropped (empty after normalization)", t))
			continue
		}
		if changed {
			warnings = append(warnings, fmt.Sprintf("topic %q → %q", t, n))
		}
		out = append(out, n)
	}
	return out, warnings
}

// FirstProjectDescription returns the project description from the first metadata target —
// for injecting into badges/build context.
func FirstProjectDescription(cfg *config.Config) string {
	// Prefer the first-class metadata: block (the settled single source of identity);
	// fall back to a legacy inline kind:metadata target description for un-migrated configs.
	if d := cfg.Metadata.Description.Default.First(); d != "" {
		return d
	}
	for _, t := range cfg.Targets {
		if t.Kind == "metadata" && t.Description.First() != "" {
			return t.Description.First()
		}
	}
	return ""
}

// hashFile returns the sha256 hex of a file's contents.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func metadataStatePath(rootDir string) string {
	return filepath.Join(rootDir, ".stagefreight", "metadata-state.json")
}

// loadMetadataState reads the committed dest→hash idempotency map (empty if absent).
func loadMetadataState(rootDir string) map[string]string {
	m := map[string]string{}
	if data, err := os.ReadFile(metadataStatePath(rootDir)); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	return m
}

func saveMetadataState(rootDir string, m map[string]string) error {
	p := metadataStatePath(rootDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(data, '\n'), 0o644)
}

// registryFieldsUsed reports which metadata fields a registry provider ACTUALLY writes for
// (short, full) — mirroring each client's routing so the summary is exact, not optimistic.
// Docker Hub has two fields; Harbor/Quay have one markdown field (readme wins, short is the
// fallback); JFrog has only a short config description; the git-forge container registries
// have no description API.
func registryFieldsUsed(provider, short, full string) []string {
	switch provider {
	case "docker", "dockerhub":
		var f []string
		if short != "" {
			f = append(f, "description")
		}
		if full != "" {
			f = append(f, "readme")
		}
		return f
	case "harbor", "quay":
		if full != "" {
			return []string{"readme"} // single markdown field takes the body
		}
		if short != "" {
			return []string{"description"} // fallback when no readme
		}
		return nil
	case "jfrog":
		if short != "" {
			return []string{"description"} // config description only; no readme API
		}
		return nil
	default: // gitlab / ghcr / gitea container registries — no description API
		return nil
	}
}

func registrySetDetail(provider, short, full string) string {
	used := registryFieldsUsed(provider, short, full)
	if len(used) == 0 {
		return "skipped (no description field)"
	}
	return "set: " + strings.Join(used, ", ")
}

func forgeSetDetail(out forge.MetadataOutcome, topicWarnings []string) string {
	var b strings.Builder
	if len(out.Set) > 0 {
		b.WriteString("set: " + strings.Join(out.Set, ", "))
	} else {
		b.WriteString("no fields")
	}
	warnings := append(append([]string(nil), topicWarnings...), out.Warnings...)
	if len(warnings) > 0 {
		b.WriteString("  ⚠ " + strings.Join(warnings, "; "))
	}
	if len(out.Skipped) > 0 {
		b.WriteString("  (skipped " + strings.Join(out.Skipped, "; ") + ")")
	}
	return b.String()
}
