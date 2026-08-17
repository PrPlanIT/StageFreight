package discovery

import (
	"os"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

// info builds a DockerFreshnessInfo with the given ARG and ENV defaults for resolver tests.
func argInfo(args, envs map[string]string) *supplychain.DockerFreshnessInfo {
	info := &supplychain.DockerFreshnessInfo{
		Args:    make(map[string]supplychain.EnvVar),
		EnvVars: make(map[string]supplychain.EnvVar),
	}
	for k, v := range args {
		info.Args[k] = supplychain.EnvVar{Name: k, Value: v, Line: 1}
	}
	for k, v := range envs {
		info.EnvVars[k] = supplychain.EnvVar{Name: k, Value: v, Line: 2}
	}
	return info
}

// A FROM that interpolates an ARG resolves to the concrete image, and the ARG line — not
// the FROM — is recorded as the editable anchor (Binding + its declaration line).
func TestResolveImageRef_ArgAnchored(t *testing.T) {
	info := argInfo(map[string]string{"ALPINE_VERSION": "3.23.5"}, nil)
	info.Args["ALPINE_VERSION"] = supplychain.EnvVar{Name: "ALPINE_VERSION", Value: "3.23.5", Line: 7}

	resolved, binding, line, ok := resolveImageRef("alpine:${ALPINE_VERSION}", info)
	if !ok {
		t.Fatal("expected resolvable")
	}
	if resolved != "alpine:3.23.5" {
		t.Errorf("resolved = %q, want alpine:3.23.5", resolved)
	}
	if binding != "ALPINE_VERSION" {
		t.Errorf("binding = %q, want ALPINE_VERSION", binding)
	}
	if line != 7 {
		t.Errorf("anchor line = %d, want 7 (the ARG line)", line)
	}
}

// All three reference forms resolve identically when backed by an ARG default.
func TestResolveImageRef_Forms(t *testing.T) {
	info := argInfo(map[string]string{"V": "3.23.5"}, nil)
	for _, ref := range []string{"alpine:${V}", "alpine:${V:-9.9.9}", "alpine:$V"} {
		resolved, binding, _, ok := resolveImageRef(ref, info)
		if !ok || resolved != "alpine:3.23.5" || binding != "V" {
			t.Errorf("resolveImageRef(%q) = (%q, %q, ok=%v), want (alpine:3.23.5, V, true)", ref, resolved, binding, ok)
		}
	}
}

// An ENV default resolves when no ARG declares the variable.
func TestResolveImageRef_EnvFallback(t *testing.T) {
	info := argInfo(nil, map[string]string{"BASE_TAG": "3.23.5"})
	resolved, binding, _, ok := resolveImageRef("alpine:${BASE_TAG}", info)
	if !ok || resolved != "alpine:3.23.5" || binding != "BASE_TAG" {
		t.Errorf("got (%q, %q, ok=%v), want (alpine:3.23.5, BASE_TAG, true)", resolved, binding, ok)
	}
}

// An inline default with no backing ARG/ENV resolves but has no ARG anchor — the version
// lives in the FROM token, so Binding is empty and apply edits the FROM line.
func TestResolveImageRef_InlineDefaultNoBinding(t *testing.T) {
	info := argInfo(nil, nil)
	resolved, binding, line, ok := resolveImageRef("alpine:${UNDECLARED:-3.23.5}", info)
	if !ok || resolved != "alpine:3.23.5" {
		t.Fatalf("resolved = %q, ok=%v, want alpine:3.23.5", resolved, ok)
	}
	if binding != "" || line != 0 {
		t.Errorf("binding/line = (%q, %d), want empty/0 (inline default, FROM-anchored)", binding, line)
	}
}

// A reference with no ARG/ENV default and no inline default is unresolvable — the only
// legitimate reason to skip an interpolated base image.
func TestResolveImageRef_Unresolvable(t *testing.T) {
	info := argInfo(nil, nil)
	if _, _, _, ok := resolveImageRef("alpine:${MISSING}", info); ok {
		t.Error("expected unresolvable (no ARG/ENV, no inline default)")
	}
}

// End-to-end parse: a global ARG before the FROM is captured in Args and links the
// interpolated base to its declaration line.
func TestParseDockerfile_ArgFromLink(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/Dockerfile"
	content := "ARG ALPINE_VERSION=3.23.5\nFROM alpine:${ALPINE_VERSION}\nRUN apk add nginx\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := parseDockerfileForFreshness(path)
	if err != nil {
		t.Fatal(err)
	}
	ev, ok := info.Args["ALPINE_VERSION"]
	if !ok || ev.Value != "3.23.5" {
		t.Fatalf("Args[ALPINE_VERSION] = %+v, ok=%v, want value 3.23.5", ev, ok)
	}
	resolved, binding, line, ok := resolveImageRef(info.Stages[0].Image, info)
	if !ok || resolved != "alpine:3.23.5" || binding != "ALPINE_VERSION" {
		t.Errorf("resolve = (%q, %q, ok=%v), want (alpine:3.23.5, ALPINE_VERSION, true)", resolved, binding, ok)
	}
	if line != ev.Line {
		t.Errorf("anchor line = %d, want ARG line %d", line, ev.Line)
	}
}

// detectAlpineVersion resolves an ARG-based alpine base so apk resolution is not skipped.
func TestDetectAlpineVersion_ArgBased(t *testing.T) {
	info := argInfo(map[string]string{"ALPINE_VERSION": "3.23.5"}, nil)
	info.Stages = []supplychain.StageInfo{{Image: "alpine:${ALPINE_VERSION}"}}
	if got := detectAlpineVersion(info); got != "3.23.5" {
		t.Errorf("detectAlpineVersion = %q, want 3.23.5", got)
	}
}
