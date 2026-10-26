package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

var toolchainPresets = map[string]string{
	"preset/toolchains.yml":          "toolchains:\n  update: true\n  retention:\n    keep_last: 3\n",
	"preset/toolchains-scanners.yml": "toolchains:\n  want:\n    trivy: \"x\"\n    cosign: \"x\"\n",
	"preset/toolchains-gitops.yml":   "toolchains:\n  want:\n    kubectl: \"x\"\n    flux: \"x\"\n",
}

// TestToolchainWantPresets: toolchains.want composes from several presets beside the
// section's own preset (update/retention), plus inline tools, with provenance per tool.
func TestToolchainWantPresets(t *testing.T) {
	files := map[string]string{".stagefreight.yml": `version: 1
toolchains:
  preset: preset/toolchains.yml
  want:
    presets: [preset/toolchains-scanners.yml, preset/toolchains-gitops.yml]
    kustomize: "5.x"
`}
	for k, v := range toolchainPresets {
		files[k] = v
	}
	dir := writeFiles(t, files)
	cfg, _, entries, err := loadResolvedMode(filepath.Join(dir, ".stagefreight.yml"), true)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	tc := cfg.Toolchains
	for _, tool := range []string{"trivy", "cosign", "kubectl", "flux"} {
		if tc.Want[tool].Constraint != "x" {
			t.Errorf("%s = %q, want x from its preset", tool, tc.Want[tool].Constraint)
		}
	}
	if tc.Want["kustomize"].Constraint != "5.x" {
		t.Errorf("an inline tool must sit beside the composed ones, got %+v", tc.Want)
	}
	if _, bogus := tc.Want["presets"]; bogus {
		t.Error("presets: must not decode as a tool")
	}
	if tc.Update == nil || !*tc.Update || tc.Retention.KeepLast != 3 {
		t.Errorf("the section preset's update/retention must survive, got %+v / %+v", tc.Update, tc.Retention)
	}
	var sawGitops bool
	for _, e := range entries {
		if e.Path == "toolchains.want.kubectl" && e.Source == "preset:preset/toolchains-gitops.yml" {
			sawGitops = true
		}
	}
	if !sawGitops {
		t.Error("provenance must record kubectl as contributed by the gitops preset")
	}
}

// TestToolchainWantPresets_Duplicate: a tool declared by two presets fails, naming both.
func TestToolchainWantPresets_Duplicate(t *testing.T) {
	files := map[string]string{
		".stagefreight.yml": "version: 1\ntoolchains:\n  want:\n    presets: [preset/toolchains-scanners.yml, preset/dup.yml]\n",
		"preset/dup.yml":    "toolchains:\n  want:\n    trivy: \"0.x\"\n",
	}
	for k, v := range toolchainPresets {
		files[k] = v
	}
	dir := writeFiles(t, files)
	_, _, _, err := loadResolvedMode(filepath.Join(dir, ".stagefreight.yml"), true)
	if err == nil || !strings.Contains(err.Error(), `duplicate key "trivy"`) ||
		!strings.Contains(err.Error(), "toolchains-scanners.yml") || !strings.Contains(err.Error(), "dup.yml") {
		t.Errorf("want a duplicate error naming both presets, got %v", err)
	}
}
