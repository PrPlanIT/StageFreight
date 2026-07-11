package version

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/PrPlanIT/StageFreight/src/supplychain"

	masterminds "github.com/Masterminds/semver/v3"
)

// LatestSatisfying returns the highest version in `available` that satisfies the
// raw manifest `constraint`, HONORING its operator: "^1.8"/"~1.2"/"=1.8.0"/">=1, <2"/
// "1.8.*" all mean what the ecosystem says. A bare version ("1.8.0", no operator)
// follows the caret convention (Cargo/npm treat it as ^1.8.0). Empty if the
// constraint is unparseable or nothing in `available` satisfies it. Yanked/prerelease
// filtering is the caller's job. This is the "honor the native constraint" selector —
// it replaces treating every manifest pin as a caret.
func LatestSatisfying(constraint string, available []string) string {
	c := caretIfBare(constraint)
	constr, err := masterminds.NewConstraint(c)
	if err != nil {
		return ""
	}
	var best *masterminds.Version
	var bestRaw string
	for _, raw := range available {
		ver, err := masterminds.NewVersion(strings.TrimSpace(raw))
		if err != nil {
			continue
		}
		if constr.Check(ver) && (best == nil || ver.GreaterThan(best)) {
			best, bestRaw = ver, raw
		}
	}
	return bestRaw
}

// caretIfBare applies the "bare version means caret" convention: a plain numeric
// version ("1.8.0") becomes "^1.8.0"; anything carrying an operator, wildcard, or
// range ("=1.8.0", "~1.2", "1.8.*", ">=1, <2") passes through unchanged.
func caretIfBare(c string) string {
	c = strings.TrimPrefix(strings.TrimSpace(c), "v")
	if c == "" {
		return c
	}
	for _, r := range c {
		if !((r >= '0' && r <= '9') || r == '.') {
			return c // has an operator / wildcard / range — honor it verbatim
		}
	}
	return "^" + c
}

// LatestEligibleSemver returns the highest version in `available` compatible with
// the caret range of a BARE `current` (^current). Retained as the caret-convenience
// form; it delegates to LatestSatisfying, which also honors explicit operators.
func LatestEligibleSemver(current string, available []string) string {
	return LatestSatisfying(current, available)
}

// Satisfies reports whether a SPECIFIC version v meets the raw manifest constraint
// (operator honored, bare = caret). The version-level counterpart of LatestSatisfying,
// used to test one candidate — e.g. an advisory's fixed-in — against declared policy.
func Satisfies(constraint, v string) bool {
	constr, err := masterminds.NewConstraint(caretIfBare(constraint))
	if err != nil {
		return false
	}
	ver, err := masterminds.NewVersion(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "v")))
	if err != nil {
		return false
	}
	return constr.Check(ver)
}

// IsPrerelease reports whether v carries a semver pre-release component (e.g.
// "1.2.0-rc1"). Unparseable versions are treated as non-prerelease.
func IsPrerelease(v string) bool {
	pv := ParseVersion(v)
	return pv != nil && pv.Prerelease() != ""
}

// VersionDelta describes how far behind a dependency is.
type VersionDelta struct {
	Major int
	Minor int
	Patch int
}

// IsZero returns true when there is no version difference.
func (d VersionDelta) IsZero() bool {
	return d.Major == 0 && d.Minor == 0 && d.Patch == 0
}

// DecomposedTag holds the semver portion and any suffix of a container tag.
// Example: "1.25-alpine" → Version "1.25.0", Suffix "alpine", Family "alpine"
//
//	"3.22.1"      → Version "3.22.1", Suffix "",       Family ""
//	"noble"       → nil Version (non-versioned)
//	"2026.1.30-ad42b553b" → Version "2026.1.30", Suffix "ad42b553b", Family ""
//
// Suffix is preserved as the raw string after the first hyphen for downstream
// consumers (detectAlpineVersion, detectDebianDistro). Family is a normalized
// key used only for tag grouping — it strips per-release metadata like commit
// hashes, rebuild numbers, and pre-release counters.
type DecomposedTag struct {
	Version   *masterminds.Version
	Suffix    string // raw suffix after first hyphen (unchanged for downstream consumers)
	Family    string // normalized family key for grouping
	PreRank   int    // pre-release rank: 0=stable, 1=rc, 2=beta, 3=alpha, 4=dev
	PreNum    int    // pre-release number: beta17 → 17
	Precision int    // numeric components in the ORIGINAL version token: "8"→1, "8.3"→2, "8.3.1"→3
	Raw       string
}

// DecomposeTag splits a tag string into its semver version, suffix, and
// normalized family key. Returns a non-nil Version when the tag starts with
// a parseable version.
//
// Classification pipeline (ordered — first match wins):
//  1. MinIO RELEASE.YYYY-MM-DDTHH-MM-SSZ → encoded as semver date
//  2. sha-<hash> prefix → non-versioned, Family "sha"
//  3. Standard decomposition with progressive parsing for 4+ dot versions
func DecomposeTag(tag string) DecomposedTag {
	dt := DecomposedTag{Raw: tag}

	// Stage 1: MinIO RELEASE detection.
	// RELEASE.2025-09-07T16-13-09Z → Version encoded as YYYYMMDD.HHMMSS.0
	if strings.HasPrefix(tag, "RELEASE.") {
		dt.Version = parseMinIORelease(tag)
		// Family is empty (all RELEASE tags group together).
		return dt
	}

	// Stage 2: sha- prefix detection.
	// sha-37e807f, sha-37e807f-alpine → non-versioned.
	if strings.HasPrefix(tag, "sha-") {
		dt.Suffix = tag
		dt.Family = "sha"
		return dt
	}

	// Stage 3: Standard decomposition.
	clean := tag
	if strings.HasPrefix(clean, "v") {
		clean = clean[1:]
	}

	// Split on first hyphen to separate version from suffix.
	// "1.25-alpine" → ("1.25", "alpine")
	// "1.25.0"      → ("1.25.0", "")
	versionPart := clean
	if idx := strings.IndexByte(clean, '-'); idx >= 0 {
		versionPart = clean[:idx]
		dt.Suffix = clean[idx+1:]
	}

	// Capture how many numeric components the tag pins (semver normalization
	// below would lose this — "8.3" and "8.3.0" both become 8.3.0).
	dt.Precision = countVersionPrecision(versionPart)

	// Attempt semver parse.
	v, err := masterminds.NewVersion(versionPart)
	if err == nil {
		dt.Version = v
	} else {
		// Progressive parsing for 4+ dot tags (e.g. Plex "1.40.2.8395").
		// Try trimming rightmost dot-segments until semver succeeds.
		v, leftover := progressiveParse(versionPart)
		if v != nil {
			dt.Version = v
			// Prepend leftover segments to suffix for family normalization.
			if leftover != "" {
				if dt.Suffix != "" {
					dt.Suffix = leftover + "-" + dt.Suffix
				} else {
					dt.Suffix = leftover
				}
			}
		}
	}

	// Normalize family from raw suffix and detect pre-release.
	dt.Family = normalizeFamily(dt.Suffix)
	dt.PreRank, dt.PreNum = detectPreRelease(dt.Suffix)

	return dt
}

// parseMinIORelease parses "RELEASE.2025-09-07T16-13-09Z" into a semver
// version encoded as Major=YYYYMMDD, Minor=HHMMSS, Patch=0.
func parseMinIORelease(tag string) *masterminds.Version {
	// Expected format: RELEASE.YYYY-MM-DDTHH-MM-SSZ
	s := strings.TrimPrefix(tag, "RELEASE.")
	s = strings.TrimSuffix(s, "Z")
	// "2025-09-07T16-13-09" → date="2025-09-07", time="16-13-09"
	parts := strings.SplitN(s, "T", 2)
	if len(parts) != 2 {
		return nil
	}
	datePart := strings.ReplaceAll(parts[0], "-", "")
	timePart := strings.ReplaceAll(parts[1], "-", "")
	// Validate: datePart should be 8 digits, timePart should be 6 digits.
	if len(datePart) != 8 || len(timePart) != 6 {
		return nil
	}
	verStr := fmt.Sprintf("%s.%s.0", datePart, timePart)
	v, err := masterminds.NewVersion(verStr)
	if err != nil {
		return nil
	}
	return v
}

// progressiveParse tries to parse versionPart as semver by progressively
// trimming rightmost dot-separated segments. Returns the parsed version
// and any leftover segments (joined by dots) that were trimmed.
// Example: "1.40.2.8395" → v=1.40.2, leftover="8395"
func progressiveParse(versionPart string) (*masterminds.Version, string) {
	segments := strings.Split(versionPart, ".")
	if len(segments) <= 3 {
		return nil, ""
	}
	// Try removing segments from the right until we get 3 or fewer.
	for end := len(segments) - 1; end >= 3; end-- {
		candidate := strings.Join(segments[:end], ".")
		v, err := masterminds.NewVersion(candidate)
		if err == nil {
			leftover := strings.Join(segments[end:], ".")
			return v, leftover
		}
	}
	// Try the first 3 segments explicitly.
	candidate := strings.Join(segments[:3], ".")
	v, err := masterminds.NewVersion(candidate)
	if err == nil {
		leftover := strings.Join(segments[3:], ".")
		return v, leftover
	}
	return nil, ""
}

// normalizeFamily converts a raw suffix into a stable grouping key by
// stripping per-release metadata (commit hashes, rebuild numbers,
// pre-release counters, version-within-suffix numbers).
//
// Examples:
//
//	"alpine"       → "alpine"
//	"alpine3.22"   → "alpine"
//	"ad42b553b"    → ""         (pure hex hash)
//	"beta17"       → "beta"
//	"ls117"        → "ls"
//	"c67dce28e"    → ""         (pure hex hash)
//	"8395-c67dce28e" → ""       (numeric + hex hash)
//	"bookworm"     → "bookworm"
func normalizeFamily(suffix string) string {
	if suffix == "" {
		return ""
	}

	// Split suffix on hyphens and process each segment.
	segments := strings.Split(suffix, "-")
	var kept []string

	for _, seg := range segments {
		seg = strings.ToLower(seg)

		// Strip hex hashes (7-40 char lowercase hex).
		if isHexHash(seg) {
			continue
		}

		// Strip purely numeric segments (build numbers like "8395").
		if isAllDigits(seg) {
			continue
		}

		// Strip known pre-release + number patterns: beta17→beta, rc3→rc, ls117→ls
		cleaned := stripTrailingDigits(seg)

		// Strip version-within-suffix (e.g. "alpine3.22" → "alpine").
		// Look for a digit boundary within the segment.
		cleaned = stripEmbeddedVersion(cleaned)

		if cleaned != "" {
			kept = append(kept, cleaned)
		}
	}

	return strings.Join(kept, "-")
}

// detectPreRelease scans a suffix for pre-release indicators and returns
// the rank (0=stable, 1=rc, 2=beta, 3=alpha, 4=dev) and any trailing
// number (e.g. beta17 → rank=2, num=17).
func detectPreRelease(suffix string) (rank int, num int) {
	if suffix == "" {
		return 0, 0
	}

	lower := strings.ToLower(suffix)

	// Check each segment for pre-release indicators.
	// Order matters: check most specific patterns first.
	type prePattern struct {
		prefix string
		rank   int
	}
	patterns := []prePattern{
		{"rc", 1},
		{"beta", 2},
		{"alpha", 3},
		{"dev", 4},
	}

	for _, seg := range strings.Split(lower, "-") {
		for _, p := range patterns {
			if strings.HasPrefix(seg, p.prefix) {
				n := extractTrailingNumber(seg[len(p.prefix):])
				return p.rank, n
			}
		}
	}

	return 0, 0
}

// isHexHash returns true if s is a 7-40 character lowercase hex string.
func isHexHash(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// isAllDigits returns true if s is non-empty and contains only digits.
func isAllDigits(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// stripTrailingDigits removes trailing digits from a string.
// "beta17" → "beta", "alpine" → "alpine", "ls117" → "ls"
func stripTrailingDigits(s string) string {
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	return s[:i]
}

// stripEmbeddedVersion strips version-like numbers embedded after an alpha
// prefix. "alpine3.22" → "alpine", "noble" → "noble"
func stripEmbeddedVersion(s string) string {
	// Find where the first digit appears.
	for i, c := range s {
		if unicode.IsDigit(c) && i > 0 {
			return s[:i]
		}
	}
	return s
}

// extractTrailingNumber parses a leading numeric string and returns its
// integer value. "" → 0, "17" → 17, ".1" → 1
func extractTrailingNumber(s string) int {
	s = strings.TrimPrefix(s, ".")
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		} else {
			break
		}
	}
	return n
}

// compareVersionStrings compares two bare version strings (no tag suffix).
func compareVersionStrings(current, latest string) VersionDelta {
	cur := ParseVersion(current)
	lat := ParseVersion(latest)
	if cur == nil || lat == nil {
		return VersionDelta{}
	}
	return VersionDelta{
		Major: int(lat.Major()) - int(cur.Major()),
		Minor: int(lat.Minor()) - int(cur.Minor()),
		Patch: int(lat.Patch()) - int(cur.Patch()),
	}
}

// CompareVersions reports whether version v sorts before (-1), equal to (0), or after
// (1) target, using ecosystem-aware semver. Unparseable inputs compare equal (0).
// Exported for the dependency remediator, which must decide whether a resolved
// transitive dependency has reached an advisory's fixed version.
func CompareVersions(v, target, ecosystem string) int {
	d := compareDependencyVersions(target, v, ecosystem) // componentwise (v - target)
	switch {
	case d.Major != 0:
		if d.Major < 0 {
			return -1
		}
		return 1
	case d.Minor != 0:
		if d.Minor < 0 {
			return -1
		}
		return 1
	case d.Patch != 0:
		if d.Patch < 0 {
			return -1
		}
		return 1
	}
	return 0
}

// compareDependencyVersions dispatches to ecosystem-aware version comparison.
// APK and APT versions have packaging-specific suffixes that must be stripped
// before semver comparison; all other ecosystems use plain semver.
func compareDependencyVersions(current, latest, ecosystem string) VersionDelta {
	switch ecosystem {
	case supplychain.EcosystemAlpineAPK:
		return compareAPKVersions(current, latest)
	case supplychain.EcosystemDebianAPT:
		return compareAPTVersions(current, latest)
	default:
		return compareVersionStrings(current, latest)
	}
}

// compareAPKVersions compares Alpine APK version strings.
// APK format: <upstream_version>-r<apk_revision>
// Examples: "3.6.1-r0", "2.9-r0", "1.2.3_alpha1-r0"
// Strips the -rN revision suffix and any _alpha/_beta/_rc/_pre/_p suffixes
// before parsing as semver. If upstream versions match but revisions differ,
// returns Patch=1 to flag the revision bump.
func compareAPKVersions(current, latest string) VersionDelta {
	curUp, curRev := splitAPKRevision(current)
	latUp, latRev := splitAPKRevision(latest)

	curVer := ParseVersion(stripAPKSuffixes(curUp))
	latVer := ParseVersion(stripAPKSuffixes(latUp))

	if curVer == nil || latVer == nil {
		return VersionDelta{}
	}

	delta := VersionDelta{
		Major: int(latVer.Major()) - int(curVer.Major()),
		Minor: int(latVer.Minor()) - int(curVer.Minor()),
		Patch: int(latVer.Patch()) - int(curVer.Patch()),
	}

	// If upstream versions are identical but the APK revision bumped,
	// treat it as a patch-level update (revision bumps carry security fixes).
	if delta.IsZero() && curRev != latRev {
		delta.Patch = 1
	}

	return delta
}

// splitAPKRevision splits "3.6.1-r0" into ("3.6.1", "r0").
// If no -rN suffix is found, returns the full string and "".
func splitAPKRevision(ver string) (upstream, revision string) {
	// Find the last "-r" followed by digits.
	for i := len(ver) - 1; i >= 2; i-- {
		if ver[i-1] == 'r' && ver[i-2] == '-' {
			tail := ver[i:]
			allDigits := true
			for _, c := range tail {
				if c < '0' || c > '9' {
					allDigits = false
					break
				}
			}
			if allDigits && len(tail) > 0 {
				return ver[:i-2], ver[i-1:]
			}
		}
	}
	return ver, ""
}

// stripAPKSuffixes removes Alpine-specific pre-release suffixes
// (_alpha, _beta, _rc, _pre, _p) that aren't valid semver.
func stripAPKSuffixes(ver string) string {
	for _, suffix := range []string{"_alpha", "_beta", "_rc", "_pre", "_p"} {
		if idx := strings.Index(ver, suffix); idx >= 0 {
			return ver[:idx]
		}
	}
	return ver
}

// compareAPTVersions compares Debian/Ubuntu APT version strings.
// APT format: [<epoch>:]<upstream_version>[-<debian_revision>]
// Examples: "1:2.3.4-1", "2.3.4-1build1", "1.2.3+dfsg-4", "3.6-2"
// Strips epoch prefix and debian revision suffix before parsing as semver.
// Epoch differences are treated as major updates. If upstream versions
// match but revisions differ, returns Patch=1.
func compareAPTVersions(current, latest string) VersionDelta {
	curEpoch, curUp, curRev := splitAPTVersion(current)
	latEpoch, latUp, latRev := splitAPTVersion(latest)

	curVer := ParseVersion(stripAPTSuffixes(curUp))
	latVer := ParseVersion(stripAPTSuffixes(latUp))

	if curVer == nil || latVer == nil {
		return VersionDelta{}
	}

	delta := VersionDelta{
		Major: int(latVer.Major()) - int(curVer.Major()),
		Minor: int(latVer.Minor()) - int(curVer.Minor()),
		Patch: int(latVer.Patch()) - int(curVer.Patch()),
	}

	// Epoch change trumps everything — treat as a major bump.
	if curEpoch != latEpoch {
		epochDelta := latEpoch - curEpoch
		if epochDelta > 0 {
			delta.Major = epochDelta
		}
	}

	// If upstream versions are identical but the debian revision bumped,
	// treat it as a patch-level update.
	if delta.IsZero() && curRev != latRev {
		delta.Patch = 1
	}

	return delta
}

// splitAPTVersion splits "1:2.3.4-1build1" into (epoch=1, upstream="2.3.4", revision="1build1").
// If no epoch, epoch=0. If no revision, revision="".
func splitAPTVersion(ver string) (epoch int, upstream, revision string) {
	// Extract epoch (everything before the first ':').
	if idx := strings.IndexByte(ver, ':'); idx >= 0 {
		for _, c := range ver[:idx] {
			if c >= '0' && c <= '9' {
				epoch = epoch*10 + int(c-'0')
			}
		}
		ver = ver[idx+1:]
	}

	// Split upstream from debian revision at the last hyphen.
	if idx := strings.LastIndexByte(ver, '-'); idx >= 0 {
		upstream = ver[:idx]
		revision = ver[idx+1:]
	} else {
		upstream = ver
	}

	return
}

// stripAPTSuffixes removes Debian-specific version decorations
// (+dfsg, +deb, +really, +b, ~bpo, etc.) that aren't valid semver.
func stripAPTSuffixes(ver string) string {
	for _, sep := range []string{"+dfsg", "+deb", "+really", "+b", "~bpo", "~"} {
		if idx := strings.Index(ver, sep); idx >= 0 {
			return ver[:idx]
		}
	}
	return ver
}

// ParseVersion attempts to parse a version string, stripping leading 'v'.
func ParseVersion(s string) *masterminds.Version {
	clean := strings.TrimPrefix(s, "v")
	v, err := masterminds.NewVersion(clean)
	if err != nil {
		return nil
	}
	return v
}

// countVersionPrecision returns the number of leading dot-separated numeric
// components in a version token: "8"→1, "8.3"→2, "8.3.1"→3, "1.40.2.8395"→4.
// Stops at the first non-numeric segment, so "noble"→0.
func countVersionPrecision(versionPart string) int {
	n := 0
	for _, seg := range strings.Split(versionPart, ".") {
		if !isAllDigits(seg) {
			break
		}
		n++
	}
	return n
}

// IsVersionLike reports whether value looks like a semver/numeric version,
// optionally with a leading "v" (e.g. "1.2.3", "8.3", "v0.31.1"). Branch names
// and arbitrary refs ("develop", "master", "main") are NOT version-like and must
// not be treated as updatable dependencies.
func IsVersionLike(value string) bool {
	v := strings.TrimSpace(value)
	v = strings.TrimPrefix(v, "v")
	if v == "" || v[0] < '0' || v[0] > '9' {
		return false
	}
	_, err := masterminds.NewVersion(v)
	return err == nil
}

// FilterTagsByVersionLine returns tags that pin the SAME version line and the
// EXACT same variant suffix as current. The version line is defined by current's
// precision: precision 1 ("8") constrains to the same major; precision ≥2
// ("8.3", "8.3.1") constrains to the same major AND minor. This respects the
// line the current tag pins and preserves its variant — so "8.3-fpm-alpine"
// never jumps to "8.4"/"8.5" and never changes to "fpm-alpine3.23". Tilde-style
// (version-line) semantics, distinct from the caret range used for Go/Cargo.
func FilterTagsByVersionLine(tags []string, current DecomposedTag) []DecomposedTag {
	var out []DecomposedTag
	if current.Version == nil {
		return out
	}
	depth := current.Precision
	if depth > 2 {
		depth = 2
	}
	for _, t := range tags {
		dt := DecomposeTag(t)
		if dt.Version == nil || IsDateLikeVersion(dt.Version) {
			continue
		}
		// Exact same variant suffix (keep "fpm-alpine", reject "fpm-alpine3.23").
		if dt.Suffix != current.Suffix {
			continue
		}
		if depth >= 1 && dt.Version.Major() != current.Version.Major() {
			continue
		}
		if depth >= 2 && dt.Version.Minor() != current.Version.Minor() {
			continue
		}
		out = append(out, dt)
	}
	return out
}

// FilterTagsByFamily returns tags from the list that share the same normalized
// family key, excluding date-like tags (e.g. "20220328") that aren't real semver.
func FilterTagsByFamily(tags []string, family string) []DecomposedTag {
	var out []DecomposedTag
	for _, t := range tags {
		dt := DecomposeTag(t)
		if dt.Version != nil && dt.Family == family && !IsDateLikeVersion(dt.Version) {
			out = append(out, dt)
		}
	}
	return out
}

// IsDateLikeVersion returns true if the version looks like a date (YYYYMMDD)
// rather than real semver. These show up in Docker Hub tags for Alpine, Ubuntu,
// etc. and would otherwise win any semver comparison (20220328.0.0 > 3.22.1).
func IsDateLikeVersion(v *masterminds.Version) bool {
	// Date tags are single-component numbers >= 19700101 with no minor/patch.
	return v.Minor() == 0 && v.Patch() == 0 && v.Major() >= 19700101
}

// LatestInFamily finds the best version among decomposed tags, preferring
// stable releases over pre-releases at the same version.
func LatestInFamily(tags []DecomposedTag) *DecomposedTag {
	if len(tags) == 0 {
		return nil
	}
	best := &tags[0]
	for i := 1; i < len(tags); i++ {
		if tagNewer(&tags[i], best) {
			best = &tags[i]
		}
	}
	return best
}

// tagNewer returns true if a should be preferred over b.
// Stable releases always beat pre-releases at the same version.
// Among pre-releases at the same version, higher rank (lower number) wins,
// then higher pre-release number wins.
func tagNewer(a, b *DecomposedTag) bool {
	if a.Version.GreaterThan(b.Version) {
		return true
	}
	if b.Version.GreaterThan(a.Version) {
		return false
	}
	// Same version — prefer stable over pre-release.
	if a.PreRank != b.PreRank {
		return a.PreRank < b.PreRank // 0 (stable) < 1 (rc) < 2 (beta)
	}
	// Same rank — higher number wins (beta17 > beta13).
	return a.PreNum > b.PreNum
}

// CompareDependencyVersions is the exported form of compareDependencyVersions.
// It dispatches to ecosystem-aware version comparison and returns a VersionDelta.
func CompareDependencyVersions(current, latest, ecosystem string) VersionDelta {
	return compareDependencyVersions(current, latest, ecosystem)
}

// DominantUpdateType is the exported form of dominantUpdateType.
// It returns "major", "minor", or "patch" for the highest-priority axis in a delta.
func DominantUpdateType(d VersionDelta) string {
	return dominantUpdateType(d)
}

// dominantUpdateType returns "major", "minor", or "patch" for the
// highest-priority axis in a delta.
func dominantUpdateType(d VersionDelta) string {
	if d.Major > 0 {
		return "major"
	}
	if d.Minor > 0 {
		return "minor"
	}
	return "patch"
}
