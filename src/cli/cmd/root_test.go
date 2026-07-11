package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

// TestPersistentPreRun_ConfigFreeCommands verifies that binary/host commands
// (version, update, du) run WITHOUT loading the project config, so a broken or
// newer-schema .stagefreight.yml never blocks them. This is the bootstrap-trap
// guard: `update` replaces a binary that may be too old to parse the very config
// it's failing on — gating it on that parse would make the fix unreachable.
// Config-consuming commands must still surface the parse error.
func TestPersistentPreRun_ConfigFreeCommands(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, ".stagefreight.yml")
	// Unterminated flow sequence — a hard YAML parse error.
	if err := os.WriteFile(bad, []byte("version: 1\ntargets: [oops\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := cfgFile
	cfgFile = bad
	t.Cleanup(func() { cfgFile = old })

	pre := rootCmd.PersistentPreRunE

	for _, name := range []string{"version", "update", "du"} {
		if err := pre(&cobra.Command{Use: name}, nil); err != nil {
			t.Errorf("%s: config-free command must not fail on a broken config, got: %v", name, err)
		}
	}

	// A config-consuming command must still fail loudly on the broken config.
	if err := pre(&cobra.Command{Use: "release"}, nil); err == nil {
		t.Error("release: expected a config-load error on a broken config, got nil")
	}
}
