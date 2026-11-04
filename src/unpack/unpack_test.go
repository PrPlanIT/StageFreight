package unpack

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

type entry struct {
	name, link, body string
	typ              byte
	mode             int64
}

func tarOf(t *testing.T, entries []entry) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		h := &tar.Header{Name: e.name, Linkname: e.link, Typeflag: e.typ, Mode: mode, Size: int64(len(e.body))}
		if e.typ != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

// A tree with a prefix, files and an in-tree symlink extracts as given, prefix stripped.
func TestTar_ExtractsTree(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "dest")
	err := Tar(tarOf(t, []entry{
		{name: "top/", typ: tar.TypeDir},
		{name: "top/bin/tool", body: "BIN", typ: tar.TypeReg, mode: 0o755},
		{name: "top/lib/x", body: "X", typ: tar.TypeReg},
		{name: "top/bin/alias", link: "../lib/x", typ: tar.TypeSymlink},
	}), dest, 1)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "bin", "alias")); err != nil || string(b) != "X" {
		t.Errorf("symlink read = %q %v", b, err)
	}
	if fi, err := os.Stat(filepath.Join(dest, "bin", "tool")); err != nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("tool mode = %v %v", fi, err)
	}
}

// Modes keep only permission bits: no setuid, setgid or sticky from an archive.
func TestTar_DropsSpecialModeBits(t *testing.T) {
	dest := t.TempDir()
	if err := Tar(tarOf(t, []entry{{name: "f", body: "x", typ: tar.TypeReg, mode: 0o4755}}), dest, 0); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(dest, "f")); err != nil || fi.Mode()&os.ModeSetuid != 0 {
		t.Fatalf("mode = %v %v, want no setuid", fi.Mode(), err)
	}
}

// An archive can't place anything outside destDir: a climbing path, an absolute or
// escaping symlink, or an entry written through a symlink the archive made.
func TestTar_RefusesEscapes(t *testing.T) {
	cases := map[string][]entry{
		"climbing path":    {{name: "x/../../evil", body: "x", typ: tar.TypeReg}},
		"absolute symlink": {{name: "x/etc", link: "/etc", typ: tar.TypeSymlink}},
		"escaping symlink": {{name: "x/up", link: "../../..", typ: tar.TypeSymlink}},
		"symlink through symlinked parent": {
			{name: "x/a/b/up", link: "../..", typ: tar.TypeSymlink},
			{name: "x/a/b/up/esc", link: "../../outside", typ: tar.TypeSymlink},
		},
		"file through symlinked parent": {
			{name: "x/a/up", link: "..", typ: tar.TypeSymlink},
			{name: "x/a/up/f", body: "x", typ: tar.TypeReg},
		},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			if err := Tar(tarOf(t, entries), filepath.Join(parent, "dest"), 1); err == nil {
				t.Fatal("extraction must be refused")
			}
		})
	}
}

// A regular file replaces a symlink at its path instead of writing through it.
func TestTar_FileReplacesSymlink(t *testing.T) {
	dest := t.TempDir()
	outside := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dest, "f")); err != nil {
		t.Fatal(err)
	}
	if err := Tar(tarOf(t, []entry{{name: "f", body: "new", typ: tar.TypeReg}}), dest, 0); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(outside); string(b) != "keep" {
		t.Fatalf("wrote through the symlink: victim = %q", b)
	}
}

func zipOf(t *testing.T, files map[string]string, links map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "a.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	for name, link := range links {
		h := &zip.FileHeader{Name: name}
		h.SetMode(os.ModeSymlink | 0o777)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(link))
	}
	zw.Close()
	f.Close()
	return path
}

// Zip entries obey the same rules: files land inside, symlinks are real symlinks only
// when they stay inside, and a climbing path is refused.
func TestZip(t *testing.T) {
	dest := t.TempDir()
	if err := Zip(zipOf(t, map[string]string{"site/index.html": "hi"}, map[string]string{"site/home.html": "index.html"}), dest); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "site", "home.html")); err != nil || string(b) != "hi" {
		t.Errorf("zip symlink = %q %v", b, err)
	}
	if err := Zip(zipOf(t, map[string]string{"../evil": "x"}, nil), t.TempDir()); err == nil {
		t.Error("a climbing zip path must be refused")
	}
	if err := Zip(zipOf(t, nil, map[string]string{"l": "/etc/passwd"}), t.TempDir()); err == nil {
		t.Error("an absolute zip symlink must be refused")
	}
}
