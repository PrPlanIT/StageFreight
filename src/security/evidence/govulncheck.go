package evidence

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// GoReachability is the govulncheck-backed reachability contributor for the "go" ecosystem —
// the first EvidenceContributor. govulncheck performs call-graph analysis over the built
// program, so it answers "can the vulnerable code be reached?", the precision layer over the
// module-level scanners. Go stdlib and toolchain advisories are covered too.
type GoReachability struct {
	// Run invokes govulncheck and returns its -json output for the module rooted at dir.
	// Injectable so the parser is unit-testable without the tool.
	Run func(ctx context.Context, dir string) ([]byte, error)
}

// NewGoReachability returns the contributor wired to the real govulncheck binary.
func NewGoReachability() *GoReachability { return &GoReachability{Run: runGovulncheck} }

func (*GoReachability) Name() string             { return "govulncheck" }
func (*GoReachability) Supports(eco string) bool { return eco == "go" }

// Contribute runs govulncheck once and maps its findings onto the vulnerabilities being
// enriched. A vulnerability govulncheck did not report a finding for gets NO evidence — it
// stays Unknown (fail-closed), never fabricated as reachable or unreachable.
func (g *GoReachability) Contribute(ctx context.Context, target Target, vulns []Vulnerability) (map[VulnRef]Evidence, error) {
	dir := ""
	if target.EcosystemDir != nil {
		dir = target.EcosystemDir["go"]
	}
	out, err := g.Run(ctx, dir)
	if err != nil {
		return nil, err
	}
	byID := parseGovulncheck(out)
	res := make(map[VulnRef]Evidence)
	for _, v := range vulns {
		// govulncheck reports under Go advisory IDs; correlate against every identifier the
		// vulnerability is known by, so a CVE- or GHSA-keyed discovery still joins its finding.
		for _, id := range v.Identifiers() {
			if ev, ok := byID[id]; ok {
				res[v.Ref()] = ev
				break
			}
		}
	}
	return res, nil
}

// govulncheck -json emits a stream of single-key objects; we consume only the "finding" ones.
type govulnMsg struct {
	Finding *govulnFinding `json:"finding"`
}
type govulnFinding struct {
	OSV   string        `json:"osv"`
	Trace []govulnFrame `json:"trace"`
}
type govulnFrame struct {
	Module   string `json:"module"`
	Package  string `json:"package"`
	Function string `json:"function"`
}

// parseGovulncheck turns govulncheck's -json stream into per-advisory reachability evidence.
// govulncheck reports a finding at the most precise level it can prove: a trace frame with a
// FUNCTION means the vulnerable symbol is CALLED (reachable); a PACKAGE-only frame means the
// package is imported but the symbol is never called; a MODULE-only frame means the module is
// required but the vulnerable package is not even imported. Only a called symbol is reachable.
func parseGovulncheck(out []byte) map[string]ReachabilityEvidence {
	res := make(map[string]ReachabilityEvidence)
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var m govulnMsg
		if err := dec.Decode(&m); err != nil {
			break // EOF or malformed tail — keep what we parsed
		}
		if m.Finding == nil || m.Finding.OSV == "" {
			continue
		}
		res[m.Finding.OSV] = findingEvidence(m.Finding)
	}
	return res
}

func findingEvidence(f *govulnFinding) ReachabilityEvidence {
	var mod, pkg, fn string
	for _, fr := range f.Trace {
		if fr.Module != "" && mod == "" {
			mod = fr.Module
		}
		if fr.Package != "" && pkg == "" {
			pkg = fr.Package
		}
		if fr.Function != "" && fn == "" {
			fn = fr.Function
		}
	}
	ev := ReachabilityEvidence{Analyzer: "govulncheck", Confidence: ConfidenceHigh}
	switch {
	case fn != "":
		ev.State = ReachReachable
		ev.Facts = []string{fmt.Sprintf("call path reaches %s", fn)}
	case pkg != "":
		ev.State = ReachUnreachable
		ev.Facts = []string{fmt.Sprintf("%s is imported but the vulnerable symbol is never called", pkg)}
	default:
		ev.State = ReachUnreachable
		ev.Facts = []string{fmt.Sprintf("module %s is required but its affected package is not imported", mod)}
	}
	return ev
}

// runGovulncheck invokes govulncheck -json over the module at dir. govulncheck exits non-zero
// when vulnerabilities are found — that is not a run error, so we parse whatever JSON it wrote;
// only an empty output signals a genuine failure.
func runGovulncheck(ctx context.Context, dir string) ([]byte, error) {
	bin, err := exec.LookPath("govulncheck")
	if err != nil {
		return nil, fmt.Errorf("govulncheck not found on PATH: %w", err)
	}
	cmd := exec.CommandContext(ctx, bin, "-json", "./...")
	if dir != "" {
		cmd.Dir = dir
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	_ = cmd.Run()
	if out.Len() == 0 {
		return nil, fmt.Errorf("govulncheck produced no output")
	}
	return out.Bytes(), nil
}
