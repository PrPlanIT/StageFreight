package freshness

import (
	"encoding/json"
	"testing"
)

func TestOsvVuln_Informational(t *testing.T) {
	// RUSTSEC unmaintained advisory (real shape): no CVSS, database_specific.informational set.
	unmaintained := `{"id":"RUSTSEC-2025-0141","summary":"Bincode is unmaintained","severity":[],"database_specific":{"informational":"unmaintained"}}`
	var v osvVuln
	if err := json.Unmarshal([]byte(unmaintained), &v); err != nil {
		t.Fatal(err)
	}
	if !v.isInformational() {
		t.Error("unmaintained advisory must be classified informational (not a vulnerability)")
	}

	// A real scored vulnerability: no informational marker.
	scored := `{"id":"RUSTSEC-2026-0104","summary":"Reachable panic","severity":[{"type":"CVSS_V3","score":"CVSS:3.1/AV:N"}]}`
	var rv osvVuln
	if err := json.Unmarshal([]byte(scored), &rv); err != nil {
		t.Fatal(err)
	}
	if rv.isInformational() {
		t.Error("a scored vulnerability must NOT be classified informational")
	}
}
