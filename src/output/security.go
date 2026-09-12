package output

import (
	"fmt"
	"sort"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/vulnerability/severity"
)

const (
	SoftBudget         = 15
	HardBudget         = 30
	AbsoluteMax        = 200
	OverwhelmThreshold = 1000

	DefaultOverwhelmLink = "https://www.psychologytoday.com/us/basics/anxiety"
)

var DefaultOverwhelmMessage = []string{"…maybe start here:"}

// VulnRow is the view model for a single vulnerability in CLI output.
type VulnRow struct {
	ID        string // "CVE-2024-45337", "GHSA-xxxx-yyyy"
	Severity  string // "CRITICAL", "HIGH", "MEDIUM", "LOW"
	Package   string // "golang.org/x/crypto"
	Installed string // "0.28.0"
	FixedIn   string // "0.31.0" (empty = no fix)
	Title     string // one-line description

	// FixedConflict marks that scanners disagree on the fix for this (CVE, package):
	// PATCHED renders as "source-specific" rather than a single version.
	FixedConflict bool
}

// ScanAudit holds metadata for the audit block at the top of the section.
type ScanAudit struct {
	Engine string // "Trivy 0.58.1"
	OS     string // "alpine 3.21.3"
}

// SecurityUX controls the >OverwhelmThreshold message/link.
// Caller is responsible for defaulting and env override behavior.
type SecurityUX struct {
	OverwhelmMessage []string
	OverwhelmLink    string
}

// ScanAuditRows renders the engine/OS audit lines (skips empty fields).
func ScanAuditRows(sec *Section, audit ScanAudit) {
	if audit.Engine != "" {
		sec.Row("%-16s%s", "engine", audit.Engine)
	}
	if audit.OS != "" {
		sec.Row("%-16s%s", "os", audit.OS)
	}
}

// SectionVulns renders the "Vulnerabilities" block as one entry per advisory (CVE).
// Packages that share a CVE collapse into the AFFECTED column, so a description is shown
// once, not once per package. Columns read AFFECTED (what is installed) → PATCHED (the
// advisory's remediation) — "what's here → what gets me out". The two are placed on one
// line only when the whole group shares a single (installed, fixed); otherwise packages
// split to their own lines so installed and fixed never drift apart. Severity-prioritized
// truncation is preserved, now counted in advisories. budget = max advisories to display.
func SectionVulns(sec *Section, vulns []VulnRow, color bool, budget int, ux SecurityUX) {
	if len(vulns) == 0 {
		return
	}

	advs := buildAdvisories(vulns)
	crit, high, med, low := advisorySeverityCounts(advs)

	sec.Row("")
	sec.Row("%s", bold(color, fmt.Sprintf(
		"Vulnerabilities  %d findings · %d advisories · %d crit · %d high · %d med · %d low",
		len(vulns), len(advs), crit, high, med, low)))
	sec.Row("")

	// Overwhelm short-circuit (huge advisory counts).
	if len(advs) > OverwhelmThreshold {
		show := 20
		if show > len(advs) {
			show = len(advs)
		}
		for i := 0; i < show; i++ {
			renderAdvisory(sec, advs[i], color)
		}
		remaining := len(advs) - show

		if len(ux.OverwhelmMessage) == 0 && ux.OverwhelmLink == "" {
			sec.Row("%s", Dimmed(fmt.Sprintf("  … and %d more advisories (see security-scan.json)", remaining), color))
			return
		}

		sec.Row("%s", Dimmed(fmt.Sprintf("  … and %d more advisories", remaining), color))
		for _, line := range ux.OverwhelmMessage {
			sec.Row("%s", Dimmed("    "+line, color))
		}
		if ux.OverwhelmLink != "" {
			sec.Row("%s", Dimmed("      "+ux.OverwhelmLink, color))
		}
		sec.Row("%s", Dimmed("  (see security-scan.json for the full list)", color))
		return
	}

	// Severity-prioritized budget walk (CRIT/HIGH advisories always shown, up to AbsoluteMax).
	emitted := 0
	hitAbsMax := false

	for _, a := range advs {
		if emitted >= AbsoluteMax {
			hitAbsMax = true
			break
		}

		r := severity.Order(severity.Normalize(a.severity))
		if r <= 1 {
			renderAdvisory(sec, a, color)
			emitted++
			continue
		}

		if emitted < budget {
			renderAdvisory(sec, a, color)
			emitted++
		}
	}

	if emitted < len(advs) {
		remaining := len(advs) - emitted
		if hitAbsMax {
			sec.Row("%s", Dimmed(fmt.Sprintf("  … and %d more advisories (hit max output %d; see security-scan.json)", remaining, AbsoluteMax), color))
		} else {
			sec.Row("%s", Dimmed(fmt.Sprintf("  … and %d more advisories (see security-scan.json)", remaining), color))
		}
	}
}

// VulnSeverityTag returns a short severity label, optionally colored.
// CRITICAL→"CRIT" red, HIGH→"HIGH" red, MEDIUM/MODERATE→"MOD " yellow,
// LOW→"LOW " gray, UNKNOWN/empty→"UNK " gray.
func VulnSeverityTag(label string, color bool) string {
	sev := severity.Normalize(label)

	tag := "UNK "
	ansi := colorGray

	switch sev {
	case "CRITICAL":
		tag, ansi = "CRIT", colorRed
	case "HIGH":
		tag, ansi = "HIGH", colorRed
	case "MEDIUM":
		tag, ansi = "MOD ", colorYellow
	case "LOW":
		tag, ansi = "LOW ", colorGray
	}

	if !color {
		return tag
	}
	return ansi + tag + colorReset
}

// VulnURL derives an advisory URL from a vulnerability ID.
// GHSA- → github.com/advisories, GO- → pkg.go.dev/vuln, default → osv.dev/vulnerability.
func VulnURL(id string) string {
	id = strings.TrimSpace(id)
	upper := strings.ToUpper(id)

	switch {
	case strings.HasPrefix(upper, "GHSA-"):
		return "https://github.com/advisories/" + id
	case strings.HasPrefix(upper, "GO-"):
		return "https://pkg.go.dev/vuln/" + id
	default:
		return "https://osv.dev/vulnerability/" + id
	}
}

// --- unexported helpers (shared by deps.go via same package) ---

func bold(color bool, s string) string {
	if !color {
		return s
	}
	return colorBold + s + colorReset
}

// advisoryPkg is one affected package + its installed/fixed versions, deduped within a CVE.
type advisoryPkg struct {
	name      string
	installed string
	fixed     string
	conflict  bool // scanners disagree on the fix → PATCHED is "source-specific"
}

// advisoryView is the per-CVE aggregation for display: the advisory identity + the set of
// affected packages that share it. The description (title) is held once per advisory.
type advisoryView struct {
	id       string
	severity string
	title    string
	pkgs     []advisoryPkg
}

// affectedColWidth is the padding target for the AFFECTED cell so PATCHED aligns for the
// common case; a longer AFFECTED simply pushes PATCHED right on that one row.
const affectedColWidth = 46

// buildAdvisories groups findings by CVE into per-advisory views: it keeps the highest
// severity seen, the longest description as the one-line title, and the deduped set of
// affected packages (name+installed+fixed). Advisories sort by severity then ID; packages
// within an advisory sort by name. This is presentation-only aggregation — no version is
// invented, and installed/fixed stay bound to their package (see renderAdvisory).
func buildAdvisories(vulns []VulnRow) []advisoryView {
	order := make([]string, 0)
	byID := make(map[string]*advisoryView)

	for _, v := range vulns {
		id := strings.TrimSpace(v.ID)
		a, ok := byID[id]
		if !ok {
			a = &advisoryView{id: id, severity: v.Severity}
			byID[id] = a
			order = append(order, id)
		}
		if severity.Order(severity.Normalize(v.Severity)) < severity.Order(severity.Normalize(a.severity)) {
			a.severity = v.Severity
		}
		if t := strings.TrimSpace(v.Title); len(t) > len(a.title) {
			a.title = t
		}
		pk := advisoryPkg{
			name:      strings.TrimSpace(v.Package),
			installed: strings.TrimSpace(v.Installed),
			fixed:     strings.TrimSpace(v.FixedIn),
			conflict:  v.FixedConflict,
		}
		dup := false
		for _, e := range a.pkgs {
			if e == pk {
				dup = true
				break
			}
		}
		if !dup {
			a.pkgs = append(a.pkgs, pk)
		}
	}

	advs := make([]advisoryView, 0, len(order))
	for _, id := range order {
		a := byID[id]
		sort.SliceStable(a.pkgs, func(i, j int) bool { return a.pkgs[i].name < a.pkgs[j].name })
		advs = append(advs, *a)
	}
	sort.SliceStable(advs, func(i, j int) bool {
		ra, rb := severity.Order(severity.Normalize(advs[i].severity)), severity.Order(severity.Normalize(advs[j].severity))
		if ra != rb {
			return ra < rb // ascending rank = descending severity
		}
		return advs[i].id < advs[j].id
	})
	return advs
}

func advisorySeverityCounts(advs []advisoryView) (crit, high, med, low int) {
	for _, a := range advs {
		switch severity.Normalize(a.severity) {
		case "CRITICAL":
			crit++
		case "HIGH":
			high++
		case "MEDIUM":
			med++
		case "LOW":
			low++
		}
	}
	return
}

// uniformPkgs reports whether every affected package shares one (installed, fixed) — the
// only case where AFFECTED and PATCHED may collapse onto a single line without implying a
// version relationship that the evidence doesn't support.
func uniformPkgs(pkgs []advisoryPkg) bool {
	if len(pkgs) <= 1 {
		return true
	}
	for _, p := range pkgs[1:] {
		if p.installed != pkgs[0].installed || p.fixed != pkgs[0].fixed || p.conflict != pkgs[0].conflict {
			return false
		}
	}
	return true
}

// patchedLabel renders the PATCHED cell from a single package's advisory fix: "→ <ver>",
// a dimmed "(no fix)" when the advisory reports none, or a dimmed "source-specific" when
// scanners disagree on the fix (the conflict is preserved, not resolved by guesswork; the
// per-source detail lives in security-scan.json). It is never an aggregate.
func patchedLabel(fixed string, conflict, color bool) string {
	if conflict {
		return Dimmed("source-specific", color)
	}
	fixed = strings.TrimSpace(fixed)
	if fixed == "" {
		return Dimmed("(no fix)", color)
	}
	return "→ " + fixed
}

// renderAdvisory prints one advisory: the SEV + CVE identity, its affected package(s) with
// installed version(s) under AFFECTED, the advisory fix under PATCHED, then one description
// line. When all packages share (installed, fixed) they collapse to a single AFFECTED →
// PATCHED line; when they differ, each package gets its own line so installed and fixed
// stay together and truthful. A trailing blank line separates advisories.
func renderAdvisory(sec *Section, a advisoryView, color bool) {
	tag := VulnSeverityTag(a.severity, color) // 4 visible chars
	cve := strings.TrimSpace(a.id)

	// Column where AFFECTED begins: "  " + tag(4) + "  " + cve(%-15s) + "  ".
	const affectedIndent = 25

	if uniformPkgs(a.pkgs) {
		names := make([]string, len(a.pkgs))
		for i, p := range a.pkgs {
			names[i] = p.name
		}
		affected := strings.Join(names, ", ")
		fixed := ""
		conflict := false
		if len(a.pkgs) > 0 {
			if a.pkgs[0].installed != "" {
				affected += " · " + a.pkgs[0].installed
			}
			fixed = a.pkgs[0].fixed
			conflict = a.pkgs[0].conflict
		}
		patched := patchedLabel(fixed, conflict, color)
		if len(affected) > affectedColWidth {
			// AFFECTED overflows its column — keep PATCHED honest (aligned under AFFECTED
			// on its own line) rather than ragged-right or truncating package names.
			sec.Row("  %s  %-15s  %s", tag, cve, affected)
			sec.Row("%s%s", strings.Repeat(" ", affectedIndent), patched)
		} else {
			sec.Row("  %s  %-15s  %-*s  %s", tag, cve, affectedColWidth, affected, patched)
		}
	} else {
		sec.Row("  %s  %s", tag, cve)
		for _, p := range a.pkgs {
			aff := p.name
			if p.installed != "" {
				aff += " · " + p.installed
			}
			sec.Row("        %-*s  %s", affectedColWidth, aff, patchedLabel(p.fixed, p.conflict, color))
		}
	}

	if a.title != "" {
		sec.RowIndented(8, true, color, "%s", a.title)
	}
	sec.Row("")
}
