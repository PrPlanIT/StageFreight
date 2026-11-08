package toolchain

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type tarEntry struct{ name, body, link string }

func gzTar(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		if e.link != "" {
			hdr = &tar.Header{Name: e.name, Mode: 0o777, Linkname: e.link, Typeflag: tar.TypeSymlink}
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if e.link == "" {
			tw.Write([]byte(e.body))
		}
	}
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

// apkFixture is a signed one-package repository served over HTTP, with its signing
// key trusted through a temporary apk keys directory.
type apkFixture struct {
	repo    apkRepo
	key     *rsa.PrivateKey
	files   map[string][]byte
	control []byte
}

const fixtureKey = "test-signer.rsa.pub"

func newAPKFixture(t *testing.T) *apkFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	keys := t.TempDir()
	os.WriteFile(filepath.Join(keys, fixtureKey), pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o644)
	old := apkKeysDir
	apkKeysDir = keys
	t.Cleanup(func() { apkKeysDir = old })

	f := &apkFixture{key: key, files: map[string][]byte{}}
	data := gzTar(t,
		tarEntry{name: "usr/lib/libfoo.so.1.2.3", body: "elf-bytes"},
		tarEntry{name: "usr/lib/libfoo.so.1", link: "libfoo.so.1.2.3"},
		tarEntry{name: "usr/share/doc/foo/README", body: "not a library"},
	)
	dataSum := sha256.Sum256(data)
	f.control = gzTar(t, tarEntry{name: ".PKGINFO", body: "pkgname = foo\ndatahash = " + hex.EncodeToString(dataSum[:]) + "\n"})
	f.files["foo-1.2.3-r0.apk"] = concat(f.sign(t, f.control, crypto.SHA1), f.control, data)

	ctlSum := sha1.Sum(f.control)
	index := "P:foo\nV:1.2.3-r0\nC:Q1" + base64.StdEncoding.EncodeToString(ctlSum[:]) + "\np:so:libfoo.so.1=1.2.3\n\nP:bar\nV:2-r0\nC:Q1" + base64.StdEncoding.EncodeToString(make([]byte, 20)) + "\np:cmd:bar=2\n\n"
	indexMember := gzTar(t, tarEntry{name: "DESCRIPTION", body: "v3.24"}, tarEntry{name: "APKINDEX", body: index})
	f.files["APKINDEX.tar.gz"] = concat(f.sign(t, indexMember, crypto.SHA256), indexMember)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := f.files[strings.TrimPrefix(r.URL.Path, "/x86_64/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	f.repo = apkRepo{url: srv.URL, arch: "x86_64"}
	return f
}

func (f *apkFixture) sign(t *testing.T, signed []byte, hash crypto.Hash) []byte {
	t.Helper()
	h := hash.New()
	h.Write(signed)
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, hash, h.Sum(nil))
	if err != nil {
		t.Fatal(err)
	}
	prefix := ".SIGN.RSA."
	if hash == crypto.SHA256 {
		prefix = ".SIGN.RSA256."
	}
	return gzTar(t, tarEntry{name: prefix + fixtureKey, body: string(sig)})
}

func concat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func TestFetchAPKIndex_VerifiesAndMapsSharedObjects(t *testing.T) {
	f := newAPKFixture(t)
	idx, err := fetchAPKIndex(f.repo)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := idx.provider("libfoo.so.1")
	if err != nil || pkg.id() != "foo-1.2.3-r0" {
		t.Fatalf("libfoo.so.1 → %+v, %v", pkg, err)
	}
	if _, ok := idx["bar"]; ok {
		t.Fatal("a cmd: provide must not be indexed as a shared object")
	}
}

func TestFetchAPKIndex_RefusesUntrustedOrTampered(t *testing.T) {
	t.Run("tampered index", func(t *testing.T) {
		f := newAPKFixture(t)
		members, _ := splitGzipMembers(f.files["APKINDEX.tar.gz"])
		forged := gzTar(t, tarEntry{name: "APKINDEX", body: "P:evil\nV:1\nC:Q1" + base64.StdEncoding.EncodeToString(make([]byte, 20)) + "\np:so:libfoo.so.1\n"})
		f.files["APKINDEX.tar.gz"] = concat(members[0], forged)
		if _, err := fetchAPKIndex(f.repo); err == nil || !strings.Contains(err.Error(), "does not verify") {
			t.Fatalf("want signature failure, got %v", err)
		}
	})
	t.Run("signer not trusted by the host", func(t *testing.T) {
		f := newAPKFixture(t)
		os.Remove(filepath.Join(apkKeysDir, fixtureKey))
		if _, err := fetchAPKIndex(f.repo); err == nil || !strings.Contains(err.Error(), "does not trust") {
			t.Fatalf("want untrusted-key failure, got %v", err)
		}
	})
	t.Run("unsigned", func(t *testing.T) {
		f := newAPKFixture(t)
		members, _ := splitGzipMembers(f.files["APKINDEX.tar.gz"])
		f.files["APKINDEX.tar.gz"] = concat(gzTar(t, tarEntry{name: "DESCRIPTION", body: "x"}), members[1])
		if _, err := fetchAPKIndex(f.repo); err == nil || !strings.Contains(err.Error(), "unsigned") {
			t.Fatalf("want unsigned failure, got %v", err)
		}
	})
}

func TestFetchAPKData_FollowsTheSignedChain(t *testing.T) {
	f := newAPKFixture(t)
	idx, err := fetchAPKIndex(f.repo)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := idx.provider("libfoo.so.1")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := fetchAPKData(f.repo, pkg)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	placed, err := extractSharedLibs(payload, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(placed) != 1 || placed[0] != "libfoo.so.1.2.3" {
		t.Fatalf("placed %v", placed)
	}
	if target, err := os.Readlink(filepath.Join(dir, "libfoo.so.1")); err != nil || target != "libfoo.so.1.2.3" {
		t.Fatalf("symlink: %q %v", target, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "README")); err == nil {
		t.Fatal("non-library file extracted")
	}

	members, _ := splitGzipMembers(f.files["foo-1.2.3-r0.apk"])
	t.Run("altered data", func(t *testing.T) {
		f.files["foo-1.2.3-r0.apk"] = concat(members[0], members[1], gzTar(t, tarEntry{name: "usr/lib/libfoo.so.1.2.3", body: "trojan"}))
		if _, err := fetchAPKData(f.repo, pkg); err == nil || !strings.Contains(err.Error(), "datahash") {
			t.Fatalf("want datahash failure, got %v", err)
		}
	})
	t.Run("altered control", func(t *testing.T) {
		ctl := gzTar(t, tarEntry{name: ".PKGINFO", body: "datahash = 00\n"})
		f.files["foo-1.2.3-r0.apk"] = concat(members[0], ctl, members[2])
		if _, err := fetchAPKData(f.repo, pkg); err == nil || !strings.Contains(err.Error(), "signed index") {
			t.Fatalf("want control failure, got %v", err)
		}
	})
}

func TestExtractSharedLibs_RefusesEscapingSymlink(t *testing.T) {
	payload := gzTar(t, tarEntry{name: "usr/lib/libfoo.so.1.2.3", body: "x"}, tarEntry{name: "usr/lib/libfoo.so.1", link: "../../etc/shadow"})
	if _, err := extractSharedLibs(payload, t.TempDir()); err == nil {
		t.Fatal("a symlink leaving the library directory must be refused")
	}
}

func TestParseAPKIndex_AmbiguityRefusedOnlyWhenNeeded(t *testing.T) {
	sum := "C:Q1" + base64.StdEncoding.EncodeToString(make([]byte, 20))
	idx, err := parseAPKIndex([]byte("P:a\nV:1\n" + sum + "\np:so:libx.so.1=1 so:liby.so.1=1\n\nP:b\nV:1\n" + sum + "\np:so:libx.so.1=1\n"))
	if err != nil {
		t.Fatalf("an index carrying duplicate providers is valid: %v", err)
	}
	if _, err := idx.provider("libx.so.1"); err == nil || !strings.Contains(err.Error(), "a, b") {
		t.Fatalf("a needed library with two providers must be refused, got %v", err)
	}
	if p, err := idx.provider("liby.so.1"); err != nil || p.name != "a" {
		t.Fatalf("liby.so.1 → %+v, %v", p, err)
	}
	if _, err := idx.provider("libz.so.1"); err == nil {
		t.Fatal("an unprovided library must be refused")
	}
}

func TestHostMainRepo(t *testing.T) {
	dir := t.TempDir()
	oldRepos, oldArch := apkReposFile, apkArchFile
	apkReposFile, apkArchFile = filepath.Join(dir, "repositories"), filepath.Join(dir, "arch")
	t.Cleanup(func() { apkReposFile, apkArchFile = oldRepos, oldArch })
	os.WriteFile(apkArchFile, []byte("aarch64\n"), 0o644)
	os.WriteFile(apkReposFile, []byte("# mirror\n@edge https://dl-cdn.alpinelinux.org/alpine/edge/main\nhttps://dl-cdn.alpinelinux.org/alpine/v3.24/main/\nhttps://dl-cdn.alpinelinux.org/alpine/v3.24/community\n"), 0o644)
	repo, err := hostMainRepo()
	if err != nil {
		t.Fatal(err)
	}
	if repo.url != "https://dl-cdn.alpinelinux.org/alpine/v3.24/main" || repo.arch != "aarch64" {
		t.Fatalf("got %+v", repo)
	}
	if got := repo.fileURL("APKINDEX.tar.gz"); got != "https://dl-cdn.alpinelinux.org/alpine/v3.24/main/aarch64/APKINDEX.tar.gz" {
		t.Fatalf("fileURL %s", got)
	}
}
