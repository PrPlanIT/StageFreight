package toolchain

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Alpine packages supply the shared libraries a musl toolchain links against but does
// not ship. The unofficial musl Node.js build is the case in point: it needs
// libstdc++.so.6 and libgcc_s.so.1, which a bare Alpine image does not carry. They are
// provisioned the way apk itself trusts them: the repository index is signed by a key
// the host already holds in /etc/apk/keys, the index records the SHA-1 of each
// package's control section, and the control section records the SHA-256 of the data
// section. Nothing unverified reaches the toolchain tree.

// The host's apk configuration, and the directories its musl loader searches by
// default. Variables so tests can point them at a fixture tree.
var (
	apkKeysDir      = "/etc/apk/keys"
	apkReposFile    = "/etc/apk/repositories"
	apkArchFile     = "/etc/apk/arch"
	hostLibraryDirs = []string{"/lib", "/usr/local/lib", "/usr/lib"}
)

// maxAPKDownload bounds an index or package download; the largest library package is
// a few MB.
const maxAPKDownload = 256 << 20

// apkRepo is one Alpine repository for one architecture.
type apkRepo struct {
	url  string // e.g. https://dl-cdn.alpinelinux.org/alpine/v3.24/main
	arch string // apk's arch token, e.g. x86_64
}

func (r apkRepo) fileURL(name string) string {
	return r.url + "/" + r.arch + "/" + name
}

// apkPackage is one record of a signed APKINDEX.
type apkPackage struct {
	name        string
	version     string
	controlSHA1 []byte
}

func (p apkPackage) id() string { return p.name + "-" + p.version }

// hostMainRepo returns the host's configured Alpine main repository, so packages come
// from the same mirror and release the host itself installs from.
func hostMainRepo() (apkRepo, error) {
	archBytes, err := os.ReadFile(apkArchFile)
	if err != nil {
		return apkRepo{}, fmt.Errorf("reading the host's apk architecture: %w", err)
	}
	arch := strings.TrimSpace(string(archBytes))
	repos, err := os.ReadFile(apkReposFile)
	if err != nil {
		return apkRepo{}, fmt.Errorf("reading the host's apk repositories: %w", err)
	}
	for _, line := range strings.Split(string(repos), "\n") {
		line = strings.TrimRight(strings.TrimSpace(line), "/")
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "@") {
			continue
		}
		if strings.HasSuffix(line, "/main") {
			return apkRepo{url: line, arch: arch}, nil
		}
	}
	return apkRepo{}, fmt.Errorf("%s names no main repository", apkReposFile)
}

// apkIndex maps each shared-object name ("libstdc++.so.6") to the packages providing it.
type apkIndex map[string][]apkPackage

// provider returns the one package providing so. A shared object offered by several
// packages is ambiguous and refused rather than picked; the index legitimately carries
// such entries (e.g. two isl major versions), so this is decided only for a needed one.
func (idx apkIndex) provider(so string) (apkPackage, error) {
	pkgs := idx[so]
	switch len(pkgs) {
	case 0:
		return apkPackage{}, errNoProvider
	case 1:
		return pkgs[0], nil
	}
	names := make([]string, len(pkgs))
	for i, p := range pkgs {
		names[i] = p.name
	}
	return apkPackage{}, fmt.Errorf("provided by several packages (%s)", strings.Join(names, ", "))
}

var errNoProvider = errors.New("no package provides it")

// fetchAPKIndex downloads the repository's APKINDEX and returns it only once its
// signature verifies against a host-trusted key.
func fetchAPKIndex(repo apkRepo) (apkIndex, error) {
	url := repo.fileURL("APKINDEX.tar.gz")
	data, err := httpGetBytes(url)
	if err != nil {
		return nil, err
	}
	members, err := splitGzipMembers(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	if len(members) != 2 {
		return nil, fmt.Errorf("%s: expected a signature and an index section, found %d sections", url, len(members))
	}
	if err := verifyAPKSignature(members[0], members[1]); err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	raw, err := readTarFile(members[1], "APKINDEX")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	return parseAPKIndex(raw)
}

// parseAPKIndex reads APKINDEX records (blank-line separated "X:value" lines) into a
// provides map.
func parseAPKIndex(raw []byte) (apkIndex, error) {
	idx := apkIndex{}
	for _, record := range strings.Split(string(raw), "\n\n") {
		var pkg apkPackage
		var provides []string
		for _, line := range strings.Split(record, "\n") {
			if len(line) < 2 || line[1] != ':' {
				continue
			}
			val := line[2:]
			switch line[0] {
			case 'P':
				pkg.name = val
			case 'V':
				pkg.version = val
			case 'C':
				if !strings.HasPrefix(val, "Q1") {
					return nil, fmt.Errorf("APKINDEX: package %s has an unsupported checksum %q", pkg.name, val)
				}
				sum, err := base64.StdEncoding.DecodeString(val[2:])
				if err != nil || len(sum) != sha1.Size {
					return nil, fmt.Errorf("APKINDEX: package %s has a malformed checksum", pkg.name)
				}
				pkg.controlSHA1 = sum
			case 'p':
				provides = strings.Fields(val)
			}
		}
		if pkg.name == "" {
			continue
		}
		for _, p := range provides {
			so, ok := strings.CutPrefix(p, "so:")
			if !ok {
				continue
			}
			so, _, _ = strings.Cut(so, "=")
			idx[so] = append(idx[so], pkg)
		}
	}
	return idx, nil
}

// fetchAPKData downloads a package and returns its data section, verified through the
// chain the signed index anchors: control section SHA-1 → index, data section SHA-256
// → the control section's datahash.
func fetchAPKData(repo apkRepo, pkg apkPackage) ([]byte, error) {
	url := repo.fileURL(pkg.id() + ".apk")
	data, err := httpGetBytes(url)
	if err != nil {
		return nil, err
	}
	members, err := splitGzipMembers(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	if len(members) != 3 {
		return nil, fmt.Errorf("%s: expected signature, control and data sections, found %d", url, len(members))
	}
	control, payload := members[1], members[2]
	if sum := sha1.Sum(control); !bytes.Equal(sum[:], pkg.controlSHA1) {
		return nil, fmt.Errorf("%s: control section does not match the signed index", url)
	}
	info, err := readTarFile(control, ".PKGINFO")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	want := pkginfoField(info, "datahash")
	if want == "" {
		return nil, fmt.Errorf("%s: control section records no datahash", url)
	}
	if sum := sha256.Sum256(payload); hex.EncodeToString(sum[:]) != want {
		return nil, fmt.Errorf("%s: data section does not match its signed datahash", url)
	}
	return payload, nil
}

// pkginfoField returns the value of a "key = value" line in .PKGINFO.
func pkginfoField(info []byte, key string) string {
	sc := bufio.NewScanner(bytes.NewReader(info))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// verifyAPKSignature checks the signature section (a tar holding one
// .SIGN.RSA.<key> or .SIGN.RSA256.<key> entry) over the signed section's compressed
// bytes, against <key> in the host's trusted key directory.
func verifyAPKSignature(sigMember, signed []byte) error {
	zr, err := gzip.NewReader(bytes.NewReader(sigMember))
	if err != nil {
		return fmt.Errorf("signature section: %w", err)
	}
	tr := tar.NewReader(zr)
	hdr, err := tr.Next()
	if err != nil {
		return fmt.Errorf("signature section: %w", err)
	}
	var hash crypto.Hash
	var keyName string
	switch {
	case strings.HasPrefix(hdr.Name, ".SIGN.RSA256."):
		hash, keyName = crypto.SHA256, strings.TrimPrefix(hdr.Name, ".SIGN.RSA256.")
	case strings.HasPrefix(hdr.Name, ".SIGN.RSA."):
		hash, keyName = crypto.SHA1, strings.TrimPrefix(hdr.Name, ".SIGN.RSA.")
	default:
		return fmt.Errorf("unsigned: first entry is %q, not a signature", hdr.Name)
	}
	if keyName == "" || strings.ContainsAny(keyName, "/\\") || keyName == "." || keyName == ".." {
		return fmt.Errorf("signature names an invalid key %q", keyName)
	}
	sig, err := io.ReadAll(io.LimitReader(tr, 64<<10))
	if err != nil {
		return fmt.Errorf("signature section: %w", err)
	}
	pemBytes, err := os.ReadFile(filepath.Join(apkKeysDir, keyName))
	if err != nil {
		return fmt.Errorf("signed by %s, which the host does not trust: %w", keyName, err)
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return fmt.Errorf("trusted key %s is not PEM", keyName)
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("trusted key %s: %w", keyName, err)
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("trusted key %s is not an RSA key", keyName)
	}
	h := hash.New()
	h.Write(signed)
	if err := rsa.VerifyPKCS1v15(rsaPub, hash, h.Sum(nil), sig); err != nil {
		return fmt.Errorf("signature by %s does not verify", keyName)
	}
	return nil
}
