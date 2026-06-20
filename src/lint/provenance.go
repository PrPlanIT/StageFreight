package lint

import (
	"bytes"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// ProvenanceKind labels where a file CAME FROM, so modules can route on origin the way
// they route on content. It exists to relax *authored-code hygiene* (trailing
// whitespace, line endings, file length) on files no human hand-maintains — without
// ever relaxing security or supply-chain inspection.
//
// Fail direction is the OPPOSITE of content classification, on purpose. Content fails
// OPEN to text (a missed signal means extra checks — just noise). Provenance fails
// CLOSED to Authored (a missed signal means full scrutiny). The dangerous mistake here
// is calling a hand-written file "generated" and then SKIPPING its checks — an evasion
// hole. So Authored is the zero value, and a file only leaves it on POSITIVE evidence.
type ProvenanceKind int

const (
	ProvenanceAuthored  ProvenanceKind = iota // hand-written (default): every check runs
	ProvenanceGenerated                       // machine-emitted: relax authored hygiene
	ProvenanceVendored                        // third-party copy: relax authored hygiene
	ProvenanceLockfile                        // dependency lock: relax hygiene, KEEP CVE scan
)

// Provenance is the classification result. Source records the evidence ("config",
// "marker", "lockfile:Cargo.lock", "vendor-marker:crates/x") for disclosure.
type Provenance struct {
	Kind   ProvenanceKind
	Source string
}

// IsAuthored reports whether the file is hand-maintained. The zero value is Authored, so
// an unclassified file is always treated as authored — absence of provenance evidence
// never suppresses a check.
func (p Provenance) IsAuthored() bool { return p.Kind == ProvenanceAuthored }

// RelaxHygiene reports whether authored-code hygiene modules (whitespace, line endings,
// line length) should stand down for this file. True for generated/vendored/lockfile.
// Security (secrets, unicode/Trojan-source), supply-chain (freshness/osv), and
// concealment modules must NOT consult this — provenance is never an evasion path.
func (p Provenance) RelaxHygiene() bool { return p.Kind != ProvenanceAuthored }

func (k ProvenanceKind) String() string {
	switch k {
	case ProvenanceGenerated:
		return "generated"
	case ProvenanceVendored:
		return "vendored"
	case ProvenanceLockfile:
		return "lockfile"
	default:
		return "authored"
	}
}

// lockfileNames are exact base names of dependency lock files — deterministic,
// machine-generated, near-zero false positive. They relax hygiene but remain
// first-class supply-chain surfaces (freshness/osv read them for CVEs).
var lockfileNames = map[string]bool{
	"Cargo.lock": true, "package-lock.json": true, "yarn.lock": true,
	"pnpm-lock.yaml": true, "npm-shrinkwrap.json": true, "go.sum": true,
	"poetry.lock": true, "Pipfile.lock": true, "Gemfile.lock": true,
	"composer.lock": true, "flake.lock": true, "pdm.lock": true,
	"bun.lockb": true, "packages.lock.json": true, "mix.lock": true,
	"pubspec.lock": true, "gradle.lockfile": true, "paket.lock": true,
}

// vendorMarkerNames are sidecar files a vendoring tool writes into a copied dependency's
// directory. Their PRESENCE marks the whole containing directory as vendored — which is
// how a third-party crate under crates/ (e.g. rqlite-rs) is distinguished from a project's
// own workspace members under the same crates/ root. Markers, never bare path globs.
var vendorMarkerNames = map[string]bool{
	".cargo_vcs_info.json": true,
	"Cargo.toml.orig":      true,
}

// generatedMarkerRe matches a self-declared generation banner. Precise on purpose: the
// well-established Go convention and the @generated token, nothing fuzzy.
var generatedMarkerRe = regexp.MustCompile(`(?i)code generated [^\n]*? do not edit|@generated`)

// generatedMarkerMaxLines bounds the banner scan to a file's first few lines. A real
// banner sits at the top (Go convention: a leading comment before package). Scanning the
// whole head would misread any file that merely MENTIONS the marker deeper down — a
// comment, a test fixture, or this very classifier — as generated.
const generatedMarkerMaxLines = 5

// hasGeneratedMarker reports a generation banner within the file's first few lines.
func hasGeneratedMarker(head []byte) bool {
	start, line := 0, 0
	for start < len(head) && line < generatedMarkerMaxLines {
		seg := head[start:]
		if nl := bytes.IndexByte(seg, '\n'); nl >= 0 {
			seg = seg[:nl]
			start += nl + 1
		} else {
			start = len(head)
		}
		if generatedMarkerRe.Match(seg) {
			return true
		}
		line++
	}
	return false
}

// vendorPathSegments are directory names that conventionally hold third-party code.
var vendorPathSegments = map[string]bool{
	"vendor": true, "third_party": true, "third-party": true,
	"external": true, "node_modules": true,
}

// deriveVendoredRoots scans the file set for vendor markers and returns the set of
// directories thereby marked vendored. Computed once over the collected paths; every
// file beneath such a directory inherits vendored provenance.
func deriveVendoredRoots(paths []string) map[string]bool {
	roots := map[string]bool{}
	for _, p := range paths {
		if vendorMarkerNames[filepath.Base(p)] {
			roots[filepath.Dir(p)] = true
		}
	}
	return roots
}

// classifyProvenance applies the confidence-ordered signal hierarchy; first match wins,
// default Authored. head is a prefix of the file's bytes (for the generated marker).
func classifyProvenance(path string, head []byte, decl config.ProvenanceConfig, vendoredRoots map[string]bool) Provenance {
	// 1) Project declarations outrank heuristics — only the project knows its own build.
	for _, g := range decl.Generated {
		if MatchGlob(g, path) {
			return Provenance{Kind: ProvenanceGenerated, Source: "config"}
		}
	}
	for _, g := range decl.Vendored {
		if MatchGlob(g, path) {
			return Provenance{Kind: ProvenanceVendored, Source: "config"}
		}
	}
	// 2) Lock files: exact, deterministic. Before the generated-marker scan so a
	//    Cargo.lock (which carries an @generated comment) gets the precise lockfile label.
	base := filepath.Base(path)
	if lockfileNames[base] {
		return Provenance{Kind: ProvenanceLockfile, Source: "lockfile:" + base}
	}
	// 3) Self-declared generation banner — at the TOP of the file only.
	if hasGeneratedMarker(head) {
		return Provenance{Kind: ProvenanceGenerated, Source: "marker"}
	}
	// 4) Vendored by directory marker (handles third-party crates under crates/).
	if root, ok := underVendoredRoot(path, vendoredRoots); ok {
		return Provenance{Kind: ProvenanceVendored, Source: "vendor-marker:" + root}
	}
	// 5) Vendored by path convention.
	if seg, ok := vendorPathSegment(path); ok {
		return Provenance{Kind: ProvenanceVendored, Source: "path:" + seg}
	}
	// 6) Fail closed.
	return Provenance{Kind: ProvenanceAuthored}
}

// underVendoredRoot walks a path's ancestor directories looking for a vendored root.
func underVendoredRoot(path string, roots map[string]bool) (string, bool) {
	if len(roots) == 0 {
		return "", false
	}
	for dir := filepath.Dir(path); dir != "." && dir != "/" && dir != ""; dir = filepath.Dir(dir) {
		if roots[dir] {
			return dir, true
		}
	}
	return "", false
}

// vendorPathSegment reports whether any path component is a conventional vendor dir.
func vendorPathSegment(path string) (string, bool) {
	for _, seg := range strings.Split(path, "/") {
		if vendorPathSegments[seg] {
			return seg, true
		}
	}
	return "", false
}
