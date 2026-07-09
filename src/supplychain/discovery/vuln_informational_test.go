package discovery

import (
	"encoding/json"
	"testing"
)

func TestOsvVuln_Informational(t *testing.T) {
	// RUSTSEC unmaintained advisory (real shape): no CVSS, informational under affected[].database_specific.
	unmaintained := `{"id":"RUSTSEC-2025-0141","summary":"Bincode is unmaintained","severity":[],
		"affected":[{"database_specific":{"categories":[],"cvss":null,"informational":"unmaintained"}}]}`
	var v osvVuln
	if err := json.Unmarshal([]byte(unmaintained), &v); err != nil {
		t.Fatal(err)
	}
	if !v.isInformational() {
		t.Error("unmaintained advisory (informational at affected level) must be classified informational")
	}

	// A real scored vulnerability: affected entry has no informational marker.
	scored := `{"id":"RUSTSEC-2026-0104","summary":"Reachable panic","severity":[{"type":"CVSS_V3","score":"CVSS:3.1/AV:N"}],
		"affected":[{"database_specific":{"cvss":"7.5"}}]}`
	var rv osvVuln
	if err := json.Unmarshal([]byte(scored), &rv); err != nil {
		t.Fatal(err)
	}
	if rv.isInformational() {
		t.Error("a scored vulnerability must NOT be classified informational")
	}
}
