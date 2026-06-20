package modules

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/lint"
)

func TestContentModule_Anomalies(t *testing.T) {
	m := &contentModule{}
	bin := func(magic string) lint.Content { return lint.Content{Kind: lint.ContentBinary, Magic: magic} }
	cases := []struct {
		name     string
		file     lint.FileInfo
		wantN    int
		wantCrit bool
	}{
		{"binary-behind-md", lint.FileInfo{Path: "notes.md", Content: bin("")}, 1, true},
		{"honest-png", lint.FileInfo{Path: "img.png", Content: bin("png")}, 0, false},
		{"png-is-jpeg", lint.FileInfo{Path: "img.png", Content: bin("jpeg")}, 1, false},
		{"honest-text", lint.FileInfo{Path: "main.go", Content: lint.Content{Kind: lint.ContentText}}, 0, false},
		{"unknown-binary-ext", lint.FileInfo{Path: "blob.bin", Content: bin("")}, 0, false},
		{"partial-binary-md", lint.FileInfo{Path: "x.md", Content: lint.Content{Kind: lint.ContentAmbiguous}}, 1, false},
		{"utf16-txt-not-flagged", lint.FileInfo{Path: "x.txt", Content: lint.Content{Kind: lint.ContentAmbiguous, Magic: "utf-16"}}, 0, false},
	}
	for _, c := range cases {
		got, err := m.Check(context.Background(), c.file)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if len(got) != c.wantN {
			t.Errorf("%s: %d findings, want %d (%+v)", c.name, len(got), c.wantN, got)
			continue
		}
		if c.wantCrit && (len(got) == 0 || got[0].Severity != lint.SeverityCritical) {
			t.Errorf("%s: expected critical", c.name)
		}
	}
}

func TestContentModule_Concealment(t *testing.T) {
	dir := t.TempDir()
	m := &contentModule{}
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	pngHdr := []byte("\x89PNG\r\n\x1a\n")
	iend := []byte("IEND\xAE\x42\x60\x82")

	// shebang disguised as a png (a script wearing a costume)
	sh := write("logo.png", []byte("#!/bin/sh\necho pwned\n"))
	if got, _ := m.Check(context.Background(), lint.FileInfo{Path: "logo.png", AbsPath: sh, Content: lint.Content{Kind: lint.ContentText}}); len(got) != 1 || got[0].Severity != lint.SeverityCritical {
		t.Errorf("shebang-as-png: want 1 critical, got %+v", got)
	}
	// clean PNG terminated by IEND → no appended-data finding
	ok := write("ok.png", append(append([]byte{}, pngHdr...), iend...))
	if got, _ := m.Check(context.Background(), lint.FileInfo{Path: "ok.png", AbsPath: ok, Content: lint.Content{Kind: lint.ContentBinary, Magic: "png"}}); len(got) != 0 {
		t.Errorf("clean png: want 0, got %+v", got)
	}
	// data appended after IEND → warning
	stego := write("stego.png", append(append(append([]byte{}, pngHdr...), iend...), []byte("HIDDEN")...))
	if got, _ := m.Check(context.Background(), lint.FileInfo{Path: "stego.png", AbsPath: stego, Content: lint.Content{Kind: lint.ContentBinary, Magic: "png"}}); len(got) != 1 || got[0].Severity != lint.SeverityWarning {
		t.Errorf("appended png: want 1 warning, got %+v", got)
	}
}

func TestTextModulesGateOnBinary(t *testing.T) {
	fi := lint.FileInfo{Path: "img.png", AbsPath: "/nonexistent", Content: lint.Content{Kind: lint.ContentBinary}}
	for _, m := range []lint.Module{&unicodeModule{}, &lineEndingsModule{}} {
		got, err := m.Check(context.Background(), fi)
		if err != nil || len(got) != 0 {
			t.Errorf("%s on binary: got %d findings, err %v — want silent", m.Name(), len(got), err)
		}
	}
}

// Keystone: a binary is COUNTED + DISCLOSED but emits NO finding. scanned ≠ emitted.
func TestEngine_BinaryScannedDisclosedSilent(t *testing.T) {
	dir := t.TempDir()
	// well-formed PNG: magic + body + IEND terminator (no appended data → clean)
	png := append(append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...), []byte("IEND\xAE\x42\x60\x82")...)
	if err := os.WriteFile(filepath.Join(dir, "img.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng, err := lint.NewEngine(config.LintConfig{}, dir, []string{"lineendings", "unicode", "content"}, nil, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	files, err := eng.CollectFiles()
	if err != nil {
		t.Fatal(err)
	}
	findings, _, err := eng.RunWithStats(context.Background(), files)
	if err != nil {
		t.Fatal(err)
	}
	if got := eng.BinariesScanned.Load(); got != 1 {
		t.Errorf("BinariesScanned = %d, want 1", got)
	}
	if len(eng.NonText) != 1 || !strings.HasSuffix(eng.NonText[0].Path, "img.png") {
		t.Errorf("NonText inventory = %+v, want [img.png]", eng.NonText)
	}
	for _, f := range findings {
		if strings.HasSuffix(f.File, "img.png") {
			t.Errorf("clean binary emitted a finding (scanned must not imply emitted): %+v", f)
		}
	}
}
