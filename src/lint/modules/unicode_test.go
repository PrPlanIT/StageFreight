package modules

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

func writeTempFile(t *testing.T, name string, content []byte) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, name)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	return path
}

func runUnicodeOnDisk(t *testing.T, opts map[string]any, logicalPath, absPath string) ([]lint.Finding, error) {
	t.Helper()

	m := &unicodeModule{}
	if err := m.Configure(opts); err != nil {
		return nil, err
	}

	fi := lint.FileInfo{
		Path:    logicalPath, // what allowlists match against
		AbsPath: absPath,     // what the module opens from disk
	}

	return m.Check(context.Background(), fi)
}

// findingMessage tries to extract a stable message string from a Finding without tightly
// coupling to the struct layout (reflection-based to survive refactors).
func findingMessage(f lint.Finding) string {
	v := reflect.ValueOf(f)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return fmt.Sprint(f)
	}

	// Prefer Message field if present.
	if field := v.FieldByName("Message"); field.IsValid() && field.CanInterface() {
		if s, ok := field.Interface().(string); ok {
			return s
		}
	}

	// Fall back to fmt.
	return fmt.Sprint(f)
}

func findingSeverityString(f lint.Finding) string {
	v := reflect.ValueOf(f)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ""
	}

	field := v.FieldByName("Severity")
	if !field.IsValid() || !field.CanInterface() {
		return ""
	}
	return fmt.Sprint(field.Interface())
}

func hasAnyFindingContaining(findings []lint.Finding, substr string) bool {
	for _, f := range findings {
		if strings.Contains(findingMessage(f), substr) {
			return true
		}
	}
	return false
}

func hasAnySeverityCritical(findings []lint.Finding) bool {
	for _, f := range findings {
		sev := strings.ToLower(findingSeverityString(f))
		// tolerate enum or string formatting differences:
		if strings.Contains(sev, "critical") {
			return true
		}
	}
	return false
}

func TestUnicode_AllowlistScopesOnlyASCIIControl(t *testing.T) {
	// Realistic ANSI: ESC[31m red ESC[0m
	content := []byte("package p\n\nvar _ = \"\x1b[31mred\x1b[0m\"\n")

	abs := writeTempFile(t, "banner_art.go", content)

	opts := map[string]any{
		"detect_bidi":          true,
		"detect_zero_width":    true,
		"detect_control_ascii": true,
		"allow_control_ascii_in_paths": []string{
			"src/output/banner_art.go",
		},
		"allow_control_ascii": []int{27}, // ESC only
	}

	// Allowed path: should suppress ASCII control findings (ESC).
	findingsAllowed, err := runUnicodeOnDisk(t, opts, "src/output/banner_art.go", abs)
	if err != nil {
		t.Fatalf("runUnicodeOnDisk(allowed): %v", err)
	}
	if hasAnyFindingContaining(findingsAllowed, "ASCII control") {
		t.Fatalf("expected no ASCII control findings on allowed path; got: %#v", findingsAllowed)
	}

	// Non-allowed path: should report ASCII control finding.
	findingsDenied, err := runUnicodeOnDisk(t, opts, "src/output/other.go", abs)
	if err != nil {
		t.Fatalf("runUnicodeOnDisk(denied): %v", err)
	}
	if !hasAnyFindingContaining(findingsDenied, "ASCII control") {
		t.Fatalf("expected ASCII control finding on non-allowed path; got: %#v", findingsDenied)
	}
}

func TestUnicode_BidiStillCriticalEvenInAllowedPath(t *testing.T) {
	// U+202E RIGHT-TO-LEFT OVERRIDE (classic trojan-source vector)
	content := []byte("package p\n\n// \u202e bidi\n")
	abs := writeTempFile(t, "bidi.go", content)

	opts := map[string]any{
		"detect_bidi":          true,
		"detect_zero_width":    true,
		"detect_control_ascii": true,
		"allow_control_ascii_in_paths": []string{
			"src/output/banner_art.go",
		},
		"allow_control_ascii": []int{27}, // ESC only
	}

	findings, err := runUnicodeOnDisk(t, opts, "src/output/banner_art.go", abs)
	if err != nil {
		t.Fatalf("runUnicodeOnDisk: %v", err)
	}
	if !hasAnySeverityCritical(findings) {
		t.Fatalf("expected a CRITICAL finding for bidi even in allowed path; got: %#v", findings)
	}
}

func TestUnicode_ZeroWidthStillCriticalEvenInAllowedPath(t *testing.T) {
	// U+200B ZERO WIDTH SPACE
	content := []byte("package p\n\n// \u200b zws\n")
	abs := writeTempFile(t, "zw.go", content)

	opts := map[string]any{
		"detect_bidi":          true,
		"detect_zero_width":    true,
		"detect_control_ascii": true,
		"allow_control_ascii_in_paths": []string{
			"src/output/banner_art.go",
		},
		"allow_control_ascii": []int{27}, // ESC only
	}

	findings, err := runUnicodeOnDisk(t, opts, "src/output/banner_art.go", abs)
	if err != nil {
		t.Fatalf("runUnicodeOnDisk: %v", err)
	}
	if !hasAnySeverityCritical(findings) {
		t.Fatalf("expected a CRITICAL finding for zero-width even in allowed path; got: %#v", findings)
	}
}

func TestUnicode_DisableControlASCIIOnly(t *testing.T) {
	ansi := []byte("package p\n\nvar _ = \"\x1b[31mred\x1b[0m\"\n")
	absANSI := writeTempFile(t, "ansi.go", ansi)

	// Disable ONLY ASCII control detection.
	opts := map[string]any{
		"detect_bidi":          true,
		"detect_zero_width":    true,
		"detect_control_ascii": false,
	}

	findingsANSI, err := runUnicodeOnDisk(t, opts, "src/cli/cmd/root.go", absANSI)
	if err != nil {
		t.Fatalf("runUnicodeOnDisk(ansi): %v", err)
	}
	if hasAnyFindingContaining(findingsANSI, "ASCII control") {
		t.Fatalf("expected no ASCII control findings when detect_control_ascii=false; got: %#v", findingsANSI)
	}

	// But bidi should still fire even with control disabled.
	bidi := []byte("package p\n\n// \u202e bidi\n")
	absBidi := writeTempFile(t, "bidi.go", bidi)

	findingsBidi, err := runUnicodeOnDisk(t, opts, "src/cli/cmd/root.go", absBidi)
	if err != nil {
		t.Fatalf("runUnicodeOnDisk(bidi): %v", err)
	}
	if !hasAnySeverityCritical(findingsBidi) {
		t.Fatalf("expected CRITICAL bidi finding even when control disabled; got: %#v", findingsBidi)
	}
}

func TestUnicode_ConfigValidation(t *testing.T) {
	// tab (9) is always-allowed; specifying it should be an error
	{
		m := &unicodeModule{}
		err := m.Configure(map[string]any{
			"allow_control_ascii": []int{9},
		})
		if err == nil {
			t.Fatalf("expected error for allow_control_ascii=[9] (tab) but got nil")
		}
	}

	// out of range
	{
		m := &unicodeModule{}
		err := m.Configure(map[string]any{
			"allow_control_ascii": []int{999},
		})
		if err == nil {
			t.Fatalf("expected error for allow_control_ascii=[999] but got nil")
		}
	}

	// negative
	{
		m := &unicodeModule{}
		err := m.Configure(map[string]any{
			"allow_control_ascii": []int{-1},
		})
		if err == nil {
			t.Fatalf("expected error for allow_control_ascii=[-1] but got nil")
		}
	}
}
