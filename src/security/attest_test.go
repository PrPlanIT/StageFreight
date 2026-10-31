package security

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAttestablePredicates(t *testing.T) {
	t.Run("SPDX SBOM + scan present", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, SBOMSPDXFile)
		write(t, dir, ScanTrivyFile)
		preds, err := AttestablePredicates(dir, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(preds) != 2 || preds[0].PredicateType != "spdxjson" || preds[1].PredicateType != "vuln" {
			t.Fatalf("got %+v", preds)
		}
	})

	t.Run("CycloneDX fallback when no SPDX", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, SBOMCycloneDXFile)
		preds, err := AttestablePredicates(dir, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(preds) != 1 || preds[0].PredicateType != "cyclonedx" {
			t.Fatalf("got %+v", preds)
		}
	})

	t.Run("SPDX preferred over CycloneDX", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, SBOMSPDXFile)
		write(t, dir, SBOMCycloneDXFile)
		preds, _ := AttestablePredicates(dir, true)
		if len(preds) != 1 || preds[0].PredicateType != "spdxjson" {
			t.Fatalf("got %+v", preds)
		}
	})

	t.Run("missing SBOM when wanted is fail-loud", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := AttestablePredicates(dir, true); err == nil {
			t.Fatal("expected error for a configured-but-missing SBOM")
		}
	})

	t.Run("missing SBOM when not wanted is silent", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, ScanTrivyFile)
		preds, err := AttestablePredicates(dir, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(preds) != 1 || preds[0].Name != "scan" {
			t.Fatalf("got %+v", preds)
		}
	})

	t.Run("empty dir, nothing wanted", func(t *testing.T) {
		preds, err := AttestablePredicates(t.TempDir(), false)
		if err != nil || len(preds) != 0 {
			t.Fatalf("got %+v err %v", preds, err)
		}
	})
}
