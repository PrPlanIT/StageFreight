package upstream

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
)

func writeFile(t *testing.T, root, name, body string) {
	t.Helper()
	p := filepath.Join(root, name)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLocatePin(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "Dockerfile", "FROM alpine\nARG ANSIBLE_CORE_VERSION=2.21.5\nENV NUT_VERSION \"v2.8.1\"\n")
	writeFile(t, root, "versions.yml", "ansible:\n  core: \"2.21.5\"  # pinned\napps:\n  - name: erpnext\n    tag: v16.33.0\n")
	writeFile(t, root, "apps.json", "{\n  \"frappe\": {\"branch\": \"version-16\", \"tag\": \"v16.50.0\"}\n}\n")
	writeFile(t, root, "Cargo.toml", "[dependencies]\n# stagefreight: upstream=jetpack\njetpack = { git = \"https://x\", tag = \"v0.5.2\" }\n")

	cases := []struct {
		pin              config.UpstreamPin
		line, col        int
		value, describes string
	}{
		{config.UpstreamPin{File: "Dockerfile", Arg: "ANSIBLE_CORE_VERSION"}, 2, 25, "2.21.5", "Dockerfile ANSIBLE_CORE_VERSION"},
		{config.UpstreamPin{File: "Dockerfile", Arg: "NUT_VERSION"}, 3, 17, "v2.8.1", "Dockerfile NUT_VERSION"},
		{config.UpstreamPin{File: "versions.yml", Path: "ansible.core"}, 2, 9, "2.21.5", "versions.yml ansible.core"},
		{config.UpstreamPin{File: "versions.yml", Path: "apps.0.tag"}, 5, 9, "v16.33.0", "versions.yml apps.0.tag"},
		{config.UpstreamPin{File: "apps.json", Path: "frappe.tag"}, 2, 45, "v16.50.0", "apps.json frappe.tag"},
		{config.UpstreamPin{File: "Cargo.toml", Marker: true}, 3, 39, "0.5.2", "Cargo.toml:3"},
	}
	for _, c := range cases {
		pv, err := LocatePin(root, "jetpack", c.pin)
		if err != nil {
			t.Errorf("%+v: %v", c.pin, err)
			continue
		}
		if pv.Line != c.line || pv.Column != c.col || pv.Value != c.value || pv.Describe() != c.describes {
			t.Errorf("%+v: got line %d col %d %q %q, want %d %d %q %q", c.pin, pv.Line, pv.Column, pv.Value, pv.Describe(), c.line, c.col, c.value, c.describes)
		}
	}
	for _, pin := range []config.UpstreamPin{
		{File: "Dockerfile", Arg: "MISSING"},
		{File: "versions.yml", Path: "ansible.nope"},
		{File: "versions.yml", Path: "ansible"},
		{File: "Cargo.toml", Marker: true, Match: `nothing(\d)`},
		{File: "absent.txt", Arg: "X"},
	} {
		if _, err := LocatePin(root, "jetpack", pin); err == nil {
			t.Errorf("%+v: expected an error", pin)
		}
	}
}

func TestRepinValue(t *testing.T) {
	for in, want := range map[[2]string]string{
		{"v2.21.5", "2.21.6"}:  "v2.21.6",
		{"2.21.5", "v2.21.6"}:  "2.21.6",
		{"2.21.5", "2.22.0b1"}: "2.22.0b1",
	} {
		if got := RepinValue(in[0], in[1]); got != want {
			t.Errorf("RepinValue(%s, %s) = %s, want %s", in[0], in[1], got, want)
		}
	}
}

func TestLock(t *testing.T) {
	root := t.TempDir()
	l, err := ReadLock(root)
	if err != nil || len(l.Sources) != 0 {
		t.Fatalf("a missing lock is empty: %+v %v", l, err)
	}
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if _, _, changed := l.Observe("core", []string{"2.21.6", "2.22.0"}, t0); !changed {
		t.Fatal("first sightings change the lock")
	}
	seen, _, changed := l.Observe("core", []string{"2.22.0"}, t0.Add(48*time.Hour))
	if !changed || !seen["2.22.0"].Equal(t0) {
		t.Fatalf("an earlier sighting is kept and a passed version pruned: %v changed=%v", seen, changed)
	}
	if _, _, changed := l.Observe("core", []string{"2.22.0"}, t0.Add(72*time.Hour)); changed {
		t.Fatal("nothing new, nothing changed")
	}
	if err := l.Write(root); err != nil {
		t.Fatal(err)
	}
	back, _ := ReadLock(root)
	if len(back.Sources) != 1 || back.Sources[0].FirstSeen["2.22.0"] != "2026-10-01T00:00:00Z" || back.LockfileVersion != 1 {
		t.Fatalf("round trip = %+v", back)
	}
}
