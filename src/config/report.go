package config

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// SectionState is the resolved state of one config domain section.
//
// Provenance only applies when Active == true.
// When Active == false, Provenance MUST be "none" — inactive sections have
// no provenance because they do not exist in the runtime model.
type SectionState struct {
	Name             string `json:"name"`
	Kind             string `json:"kind"`              // "execution" | "capability" | "structural"
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
	{name: "builds", kind: "execution"},
	{name: "versioning", kind: "execution"},
	{name: "lint", kind: "execution"},
	{name: "security", kind: "execution"},
	{name: "commit", kind: "execution"},
	{name: "dependency", kind: "execution"},
	{name: "docs", kind: "execution"},
	{name: "release", kind: "execution"},
	{name: "gitops", kind: "capability"},
	{name: "governance", kind: "capability"},
	{name: "glossary", kind: "capability"},
	{name: "presentation", kind: "capability"},
	{name: "manifest", kind: "capability"},
	{name: "tag", kind: "capability"},
	{name: "forges", kind: "structural"},
	{name: "repos", kind: "structural"},
	{name: "registries", kind: "structural"},
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

// LoadWithReport loads config and returns a ConfigReport with real provenance.
// Calls LoadWithWarnings for config resolution, then ResolvePresets for section provenance.
func LoadWithReport(path string) (*Config, ConfigReport, error) {
	if path == "" {
		path = defaultConfigFile
	}

	absPath, _ := filepath.Abs(path)
	report := ConfigReport{
		SourceFile:   absPath,
		Status:       "ok",
		Completeness: "complete",
	}

	cfg, warnings, err := LoadWithWarnings(path)
	if err != nil {
		report.Status = "error"
		report.Completeness = "partial"
		report.Error = err.Error()
		return nil, report, err
	}

	for _, w := range warnings {
		if strings.Contains(w, "incomplete") || strings.Contains(w, "partial") {
			report.Status = "partial"
			report.Completeness = "partial"
		}
	}
	report.Warnings = warnings

	data, readErr := os.ReadFile(path)
	if readErr != nil {
		// Config file absent — all sections inactive.
		report.Sections = buildSectionsFromMap(nil)
		return cfg, report, nil
	}

	var rawMap map[string]any
	if yamlErr := yaml.Unmarshal(data, &rawMap); yamlErr == nil {
		loader := localPresetLoader{baseDir: filepath.Dir(absPath)}
		_, entries, resolveErr := ResolvePresets(rawMap, loader, "local", absPath, 0, nil)
		if resolveErr == nil {
			report.Sections, report.Presets, report.Overrides = buildSectionsFromEntries(entries, absPath)
		} else {
			report.Status = "partial"
			report.Completeness = "partial"
			report.Sections = buildSectionsFromMap(sourceMapFromKeys(parseToplevelKeys(data)))
		}
	} else {
		report.Sections = buildSectionsFromMap(sourceMapFromKeys(parseToplevelKeys(data)))
	}

	report.VarsApplied = len(cfg.Vars)
	return cfg, report, nil
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
