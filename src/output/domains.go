package output

import (
	"fmt"
	"io"
)

// PanelDomain declares ownership boundaries for rendered data.
// No datum may appear outside its owning domain panel.
//
// This is typed guidance today: ContextBlock accepts []DomainKV (not the
// old []KV section-row type), which eliminates the structural leakage
// category. Domain value enforcement is not yet compile-time verified.
type PanelDomain string

const (
	// DomainCode owns: Commit SHA, Branch, Tag.
	// Rendered once per run as a plain ContextBlock (no box frame).
	DomainCode PanelDomain = "code"

	// DomainExecution owns: Engine, Pipeline ID, Runner name, Job ID,
	// Workflow (GitHub), Controller/Satellite (SF daemon future),
	// and all substrate facts (disk, memory, cpu, docker, buildkit).
	// Rendered as the "Runner" section box.
	DomainExecution PanelDomain = "execution"

	// DomainPlan owns: declared build intent — platforms, strategy,
	// build count, cache configuration, target declarations.
	// Registries appear here as declared push destinations (intent).
	DomainPlan PanelDomain = "plan"

	// DomainBuild owns: build execution — layers, timing, cache state,
	// builder identity. Builder info and cache info fold into Build
	// as subsections, not standalone panels.
	DomainBuild PanelDomain = "build"

	// DomainResult owns: produced artifacts and their distribution —
	// image digests, pushed tags, registry outcomes, release objects,
	// forge projections. Registries appear here as distribution outcomes.
	DomainResult PanelDomain = "result"

	// DomainSecurity owns: vulnerability findings, SBOM artifacts,
	// scanner aggregate summary. Raw scanner output is secondary artifact.
	DomainSecurity PanelDomain = "security"

	// DomainDeps owns: applied updates, skipped dependencies, CVEs fixed.
	DomainDeps PanelDomain = "deps"
)

// DomainKV is a typed KV pair with domain ownership.
// ContextBlock accepts []DomainKV, which prevents the old []KV (section row)
// type from being passed accidentally — eliminating the entire category of
// unintentional structural leakage. Note: the Go type system does not verify
// the Domain field value at compile time, so callers can still construct a
// DomainKV{Domain: DomainPlan, ...} and pass it in. This is typed guidance,
// not true domain enforcement. Full prevention would require a sealed
// constructor interface — tracked for a future hardening pass.
type DomainKV struct {
	Domain PanelDomain
	Key    string
	Value  string
}

// CodeKV constructs a DomainCode KV pair. The conventional entry point for
// ContextBlock — signals intent and makes drift visible in code review.
func CodeKV(key, value string) DomainKV {
	return DomainKV{Domain: DomainCode, Key: key, Value: value}
}

// ContextBlock prints the code identity header.
// Renders in two-column pairs per line.
func ContextBlock(w io.Writer, kv []DomainKV) {
	if len(kv) == 0 {
		return
	}
	fmt.Fprintln(w)
	for i := 0; i < len(kv); i += 2 {
		if i+1 < len(kv) {
			fmt.Fprintf(w, "    %-12s%-14s%-11s%s\n",
				kv[i].Key, kv[i].Value, kv[i+1].Key, kv[i+1].Value)
		} else {
			fmt.Fprintf(w, "    %-12s%s\n", kv[i].Key, kv[i].Value)
		}
	}
}
