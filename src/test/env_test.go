package test

import (
	"os"
	"strings"
	"testing"
)

// TestGoSuiteEnv_ProvisionedGoOnPATH pins the fix for the opaque cover failure:
// `go test -cover`/vet spawn child `go` processes, so the provisioned go's own
// directory MUST be on PATH. Without it, cover runs die with
// `exec: "go": executable file not found in $PATH` and the suite fails with no
// visible reason. The old code set PATH = os.Getenv("PATH") (no toolchain dir).
func TestGoSuiteEnv_ProvisionedGoOnPATH(t *testing.T) {
	const goPath = "/opt/sf/toolchains/go/1.25.0/bin/go"
	env := goSuiteEnv(goPath, true)

	var path, cgo string
	haveHome := false
	for _, kv := range env {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			continue
		}
		switch kv[:i] {
		case "PATH":
			path = kv[i+1:]
		case "CGO_ENABLED":
			cgo = kv[i+1:]
		case "HOME":
			haveHome = true
		}
	}

	wantPrefix := "/opt/sf/toolchains/go/1.25.0/bin" + string(os.PathListSeparator)
	if !strings.HasPrefix(path, wantPrefix) {
		t.Errorf("PATH must start with the provisioned go dir so child `go` is found\n  got:  %q\n  want prefix: %q", path, wantPrefix)
	}
	if cgo != "1" {
		t.Errorf("CGO_ENABLED = %q, want \"1\" for a -race suite", cgo)
	}
	if !haveHome {
		t.Error("expected HOME from CleanEnv to be preserved")
	}
}

// TestGoSuiteEnv_NoRaceLeavesCGOUnset: without -race we must not force CGO on.
func TestGoSuiteEnv_NoRaceLeavesCGOUnset(t *testing.T) {
	for _, kv := range goSuiteEnv("/x/go/bin/go", false) {
		if strings.HasPrefix(kv, "CGO_ENABLED=") {
			t.Errorf("CGO_ENABLED should be unset without -race; got %q", kv)
		}
	}
}
