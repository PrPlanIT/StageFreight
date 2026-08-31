package config

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/PrPlanIT/StageFreight/src/presetref"
)

// SourceFetcher is the injected remote-preset fetcher. It is nil by default — in that
// state a sourced preset ref resolves from the preset-cache only (offline), erroring on
// a miss. A network-capable entry point (the CLI) sets it at startup so tracked/pinned
// refs fetch live. This is a set-once startup seam, not a per-call mutable global.
var SourceFetcher presetref.Fetcher

// sourceAwareLoader resolves preset references: a local path reads from the working tree
// / preset-cache (the pre-existing behavior); a SOURCED ref (<source>//<path>[@<ref>])
// resolves through the source-tracking resolver — live fetch for tracked refs with the
// cache as fallback, cache-authoritative for pins. This is what reframes the committed
// preset-cache from the mandatory read path (Deliverable 0) into the fallback/pin store.
type sourceAwareLoader struct {
	local    localPresetLoader
	cacheDir string
	forges   map[string]string // forge id → base URL, from the config being loaded
	// outcomes accumulates how each sourced reference resolved, so a caller can report
	// drift and republish what it refreshed. Pointer: the loader is passed by value.
	outcomes *[]presetref.Outcome
	// offline resolves sourced references from the retained cache only.
	offline bool
}

func (l sourceAwareLoader) Load(path string) ([]byte, error) {
	ref := presetref.Parse(path)
	if ref.Kind == presetref.Local {
		return l.local.Load(path)
	}
	ref.Source = l.resolveSource(ref.Source)
	r := l.resolver()
	if l.outcomes != nil {
		r.Observe = func(o presetref.Outcome) { *l.outcomes = append(*l.outcomes, o) }
	}
	if SourceFetcher == nil || l.offline {
		// No network-capable fetcher wired: resolve from the cache only (a pre-seeded
		// pin or a previously-fetched tracked ref), erroring clearly on a miss.
		r.Mode = presetref.FetchOffline
	}
	return r.Resolve(ref)
}

// resolveSource maps a forge-shorthand preset source ("<forgeid>:<group>/<repo>") to a
// repo URL using the config's own forges: block. A URL (has a scheme) or an scp-like
// remote passes through unchanged; an unknown shorthand also passes through, leaving the
// fetcher to error clearly.
// resolver is the resolver this loader resolves sourced references with.
func (l sourceAwareLoader) resolver() presetref.Resolver {
	return presetref.Resolver{
		Fetcher:        SourceFetcher,
		Cache:          presetref.NewFSCache(l.cacheDir),
		MaxFallbackAge: presetref.DefaultMaxFallbackAge,
	}
}

func (l sourceAwareLoader) resolveSource(source string) string {
	if strings.Contains(source, "://") {
		return source // already a URL
	}
	id, rest, ok := strings.Cut(source, ":")
	if !ok {
		return source
	}
	if base, found := l.forges[id]; found {
		return strings.TrimSuffix(base, "/") + "/" + rest
	}
	return source // unknown forge id, or scp-like (git@host:path) → leave for the fetcher
}

// forgeURLsFromNode extracts forge id → url from a config's raw YAML node, so a
// forge-shorthand preset source can be resolved during load (before the struct decode).
func forgeURLsFromNode(root *yaml.Node) map[string]string {
	out := map[string]string{}
	doc := root
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i].Value != "forges" {
			continue
		}
		forges := doc.Content[i+1]
		if forges.Kind != yaml.MappingNode {
			return out
		}
		for j := 0; j+1 < len(forges.Content); j += 2 {
			id, entry := forges.Content[j].Value, forges.Content[j+1]
			if entry.Kind != yaml.MappingNode {
				continue
			}
			for k := 0; k+1 < len(entry.Content); k += 2 {
				if entry.Content[k].Value == "url" {
					out[id] = entry.Content[k+1].Value
				}
			}
		}
	}
	return out
}

// SectionState is the resolved state of one config domain section.
//
// Provenance only applies when Active == true.
// When Active == false, Provenance MUST be "none" — inactive sections have
// no provenance because they do not exist in the runtime model.
type SectionState struct {
	Name             string `json:"name"`
	Kind             string `json:"kind"` // "execution" | "capability" | "structural"
	Active           bool   `json:"active"`
	SourcePresent    bool   `json:"source_present"`
	Provenance       string `json:"provenance"`        // "manifest" | "preset" | "none"
	ResolutionStatus string `json:"resolution_status"` // "resolved" | "partial" | "none"
}

func (s SectionState) validate() string {
	if !s.Active && s.Provenance != "none" {
		return s.Name + ": inactive section has non-none provenance (" + s.Provenance + ")"
	}
	if s.Active && s.Provenance == "none" {
		return s.Name + ": active section has provenance=none"
	}
	return ""
}

// ConfigReport is the result of loading and resolving configuration.
// Surfaces the "Explain" layer of the resolution pipeline.
type ConfigReport struct {
	SourceFile   string         `json:"source_file"`
	Presets      []string       `json:"presets,omitempty"`
	Overrides    int            `json:"overrides,omitempty"`
	Sections     []SectionState `json:"sections,omitempty"`
	SyncTopology *SyncTopology  `json:"sync_topology,omitempty"`
	VarsApplied  int            `json:"vars_applied,omitempty"`
	Warnings     []string       `json:"warnings,omitempty"`
	Status       string         `json:"status"`       // "ok" | "partial" | "error"
	Completeness string         `json:"completeness"` // "complete" | "partial"
	Error        string         `json:"error,omitempty"`
}

type sectionDef struct {
	name string
	kind string
}

var allKnownSections = []sectionDef{
	{name: "git", kind: "execution"},
	{name: "builds", kind: "execution"},
	{name: "versioning", kind: "execution"},
	{name: "lint", kind: "execution"},
	{name: "security", kind: "execution"},
	{name: "commit", kind: "execution"},
	{name: "dependency", kind: "execution"},
	{name: "docs", kind: "execution"},
	{name: "release", kind: "execution"},
	{name: "publish", kind: "execution"},
	{name: "gitops", kind: "capability"},
	{name: "governance", kind: "capability"},
	{name: "glossary", kind: "capability"},
	{name: "presentation", kind: "capability"},
	{name: "manifest", kind: "capability"},
	{name: "tag", kind: "capability"},
	{name: "forges", kind: "structural"},
	{name: "repos", kind: "structural"},
	{name: "registries", kind: "structural"},
	{name: "orgs", kind: "structural"},
	{name: "metadata", kind: "structural"},
	{name: "build_cache", kind: "structural"},
	{name: "matchers", kind: "structural"},
}

// localPresetLoader loads preset files relative to a base directory.
type localPresetLoader struct {
	baseDir string
}

func (l localPresetLoader) Load(path string) ([]byte, error) {
	return os.ReadFile(filepath.Join(l.baseDir, path))
}

// dirExists reports whether path exists and is a directory. Used to detect a
// governed satellite's .stagefreight/preset-cache so preset refs resolve from it.
func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// LoadWithReport loads config and returns a ConfigReport with real provenance.
// It also returns the raw per-value merge entries so callers that want per-value
// provenance (e.g. `config resolve --verbose`) render from the SAME resolution —
// no second resolve. Entries are nil for an absent/preset-free config.
func LoadWithReport(path string) (*Config, ConfigReport, []MergeEntry, error) {
	if path == "" {
		path = defaultConfigFile
	}

	absPath, _ := filepath.Abs(path)
	report := ConfigReport{
		SourceFile:   absPath,
		Status:       "ok",
		Completeness: "complete",
	}

	// loadResolved resolves presets ONCE and returns the provenance entries — the
	// report reuses them here instead of resolving a second time (the resolve-and-
	// discard that used to live in this function is gone; there's one resolve site).
	cfg, warnings, entries, err := loadResolved(path)
	if err != nil {
		report.Status = "error"
		report.Completeness = "partial"
		report.Error = err.Error()
		return nil, report, nil, err
	}

	for _, w := range warnings {
		if strings.Contains(w, "incomplete") || strings.Contains(w, "partial") {
			report.Status = "partial"
			report.Completeness = "partial"
		}
	}
	report.Warnings = warnings

	if entries != nil {
		report.Sections, report.Presets, report.Overrides = buildSectionsFromEntries(entries, absPath)
	} else {
		// Absent file, or a resolver hiccup on a preset-free config: no provenance
		// entries — derive sections from the raw top-level keys instead.
		data, _ := os.ReadFile(path)
		report.Sections = buildSectionsFromMap(sourceMapFromKeys(parseToplevelKeys(data)))
	}

	report.VarsApplied = len(cfg.Vars)
	if topo := BuildSyncTopology(cfg); len(topo.Mirrors) > 0 {
		report.SyncTopology = &topo
	}
	return cfg, report, entries, nil
}

func buildSectionsFromEntries(entries []MergeEntry, configPath string) ([]SectionState, []string, int) {
	sectionSource := make(map[string]string)
	presetPaths := make(map[string]bool)
	overrides := 0

	for _, e := range entries {
		section := strings.SplitN(e.Path, ".", 2)[0]
		if strings.HasPrefix(e.Source, "preset:") {
			presetPath := strings.TrimPrefix(e.Source, "preset:")
			presetPaths[presetPath] = true
			if _, seen := sectionSource[section]; !seen {
				sectionSource[section] = "preset"
			}
		} else {
			if _, seen := sectionSource[section]; !seen {
				sectionSource[section] = "manifest"
			}
		}
		if e.Overridden {
			overrides++
		}
	}

	var presets []string
	for p := range presetPaths {
		presets = append(presets, p)
	}
	sort.Strings(presets)

	return buildSectionsFromMap(sectionSource), presets, overrides
}

func sourceMapFromKeys(present map[string]bool) map[string]string {
	m := make(map[string]string, len(present))
	for k := range present {
		m[k] = "manifest"
	}
	return m
}

func buildSectionsFromMap(sectionSource map[string]string) []SectionState {
	var sections []SectionState
	for _, def := range allKnownSections {
		src := sectionSource[def.name]
		active := src != ""
		provenance := src
		resStatus := "none"
		if !active {
			provenance = "none"
		} else {
			resStatus = "resolved"
		}
		ss := SectionState{
			Name:             def.name,
			Kind:             def.kind,
			Active:           active,
			SourcePresent:    src == "manifest",
			Provenance:       provenance,
			ResolutionStatus: resStatus,
		}
		if msg := ss.validate(); msg != "" {
			panic("SectionState invariant violated: " + msg)
		}
		sections = append(sections, ss)
	}
	return sections
}

func parseToplevelKeys(data []byte) map[string]bool {
	present := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		if len(line) == 0 || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
			continue
		}
		if idx := strings.Index(line, ":"); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			if key != "" {
				present[key] = true
			}
		}
	}
	return present
}
