package discovery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

// TestCorrelateVulns_BatchHydrateRetryFailLoud exercises the whole OSV path: a single
// batch query maps advisories to the right dependency, detail hydration recovers from a
// transient 503 (retry), a permanently-failing advisory marks its dependency
// VULN-UNVERIFIED (fail-loud) instead of silently clean, and OSV-ineligible ecosystems
// are skipped without being flagged.
func TestCorrelateVulns_BatchHydrateRetryFailLoud(t *testing.T) {
	var hydrateHits int32 // GHSA-test detail fetches (to prove one transient retry)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/querybatch":
			var req osvBatchRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			resp := osvBatchResponse{Results: make([]osvBatchResult, len(req.Queries))}
			for i, q := range req.Queries {
				switch q.Package.Name {
				case "vulnpkg":
					resp.Results[i].Vulns = []osvVulnRef{{ID: "GHSA-test"}}
				case "failpkg":
					resp.Results[i].Vulns = []osvVulnRef{{ID: "GHSA-fail"}}
				}
			}
			_ = json.NewEncoder(w).Encode(resp)

		case r.URL.Path == "/v1/vulns/GHSA-test":
			// Transient failure on the first hit, success on the retry — proves the
			// per-attempt deadline + bounded retry recovers rather than abandoning.
			if atomic.AddInt32(&hydrateHits, 1) == 1 {
				http.Error(w, "upstream hiccup", http.StatusServiceUnavailable)
				return
			}
			_ = json.NewEncoder(w).Encode(osvVuln{ID: "GHSA-test", Summary: "test advisory"})

		case r.URL.Path == "/v1/vulns/GHSA-fail":
			// Never recovers — the referencing dep must end UNVERIFIED, not clean.
			http.Error(w, "gone", http.StatusInternalServerError)

		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	prev := osvBaseURL
	osvBaseURL = srv.URL
	defer func() { osvBaseURL = prev }()

	m := NewResolver()
	m.http = newHTTPClient(2) // short per-attempt deadline keeps the test snappy

	deps := []supplychain.Dependency{
		{Name: "vulnpkg", Current: "1.0.0", Ecosystem: supplychain.EcosystemNpm},
		{Name: "failpkg", Current: "2.0.0", Ecosystem: supplychain.EcosystemNpm},
		{Name: "cleanpkg", Current: "3.0.0", Ecosystem: supplychain.EcosystemNpm},
		{Name: "someimage", Current: "1", Ecosystem: supplychain.EcosystemDockerImage},
	}
	m.correlateVulns(context.Background(), deps)

	// vulnpkg: one advisory, recovered after the transient 503, and NOT unverified.
	if len(deps[0].Vulnerabilities) != 1 || deps[0].Vulnerabilities[0].ID != "GHSA-test" {
		t.Fatalf("vulnpkg: want 1 vuln GHSA-test, got %+v", deps[0].Vulnerabilities)
	}
	if deps[0].VulnScanError != "" {
		t.Errorf("vulnpkg: unexpected scan error %q", deps[0].VulnScanError)
	}
	if hydrateHits < 2 {
		t.Errorf("expected GHSA-test to be retried (>=2 hits), got %d", hydrateHits)
	}

	// failpkg: hydrate never recovered → UNVERIFIED, and no phantom clean verdict.
	if deps[1].VulnScanError == "" {
		t.Errorf("failpkg: expected VulnScanError (fail-loud), got none")
	}
	if len(deps[1].Vulnerabilities) != 0 {
		t.Errorf("failpkg: unverified dep must carry no vulns, got %+v", deps[1].Vulnerabilities)
	}

	// cleanpkg: genuinely clean — no advisories, no error.
	if len(deps[2].Vulnerabilities) != 0 || deps[2].VulnScanError != "" {
		t.Errorf("cleanpkg: want clean, got vulns=%+v err=%q", deps[2].Vulnerabilities, deps[2].VulnScanError)
	}

	// docker image: OSV-ineligible ecosystem, skipped entirely (not flagged unverified).
	if deps[3].VulnScanError != "" {
		t.Errorf("someimage: OSV-ineligible ecosystem must not be flagged, got %q", deps[3].VulnScanError)
	}

	// The Snapshot coverage-gap helper reports exactly the one unverified dep.
	snap := &supplychain.Snapshot{Dependencies: deps}
	if unv := snap.UnverifiedVulns(); len(unv) != 1 || unv[0].Name != "failpkg" {
		t.Errorf("UnverifiedVulns: want [failpkg], got %v", names(unv))
	}
}

func names(deps []supplychain.Dependency) string {
	var b []string
	for _, d := range deps {
		b = append(b, d.Name)
	}
	return strings.Join(b, ",")
}
