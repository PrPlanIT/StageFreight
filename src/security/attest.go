package security

import (
	"fmt"
	"os"
	"path/filepath"
)

// Supply-chain artifact filenames written by Scan under the security output directory. Defined
// once here so the scanner that WRITES them and the attestor that READS them back share a single
// source of truth — a rename can never drift the two apart.
const (
	SBOMSPDXFile      = "sbom.spdx.json"
	SBOMCycloneDXFile = "sbom.cyclonedx.json"
	ScanTrivyFile     = "security-scan.json"
)

// AttestPredicate is one supply-chain statement to attest onto a built image digest: the local
// predicate file and the cosign --type it is attested under.
type AttestPredicate struct {
	Name          string // short label for logs/outcomes ("sbom", "scan")
	Path          string // local predicate file
	PredicateType string // cosign --type: spdxjson | cyclonedx | vuln
}

// AttestablePredicates returns the supply-chain predicates present under outputDir that should be
// attested onto the image alongside the build provenance: the SBOM (SPDX preferred, CycloneDX as
// the fallback format) and the vulnerability scan result.
//
// It is additive and present-driven — an artifact the scan did not produce is simply not attested,
// because there is genuinely nothing to attest, not because a step was skipped. The ONE fail-loud
// case is a configured-but-missing SBOM: when wantSBOM is true the run was meant to generate an
// SBOM, so its absence is a real misconfiguration — and a silently un-attested SBOM is precisely
// the gap attestation exists to close — so this errs rather than shipping the image without it.
func AttestablePredicates(outputDir string, wantSBOM bool) ([]AttestPredicate, error) {
	var preds []AttestPredicate

	spdx := filepath.Join(outputDir, SBOMSPDXFile)
	cdx := filepath.Join(outputDir, SBOMCycloneDXFile)
	switch {
	case fileExists(spdx):
		preds = append(preds, AttestPredicate{Name: "sbom", Path: spdx, PredicateType: "spdxjson"})
	case fileExists(cdx):
		preds = append(preds, AttestPredicate{Name: "sbom", Path: cdx, PredicateType: "cyclonedx"})
	case wantSBOM:
		return nil, fmt.Errorf("attestation is enabled and an SBOM was requested (security.sbom), but none was found at %s — refusing to ship an image whose SBOM is silently un-attested", spdx)
	}

	if scan := filepath.Join(outputDir, ScanTrivyFile); fileExists(scan) {
		preds = append(preds, AttestPredicate{Name: "scan", Path: scan, PredicateType: "vuln"})
	}

	return preds, nil
}

// fileExists reports whether path is an existing regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
