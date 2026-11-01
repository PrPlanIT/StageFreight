package config

import "testing"

// TestSplitForgeURL covers the provider-free split, and that ParseForgeURL keeps its
// heuristic on top of it: a neutral hostname splits cleanly but cannot be classified.
func TestSplitForgeURL(t *testing.T) {
	tests := []struct {
		raw, wantBase, wantProject string
		wantErr                    bool
	}{
		{raw: "https://git.optcp.com/PrPlanIT/MaintenancePolicy", wantBase: "https://git.optcp.com", wantProject: "PrPlanIT/MaintenancePolicy"},
		{raw: "https://gitlab.example.com/g/sub/r/", wantBase: "https://gitlab.example.com", wantProject: "g/sub/r"},
		{raw: "https://git.optcp.com", wantErr: true},
		{raw: "://bad", wantErr: true},
	}
	for _, tt := range tests {
		base, project, err := SplitForgeURL(tt.raw)
		if tt.wantErr {
			if err == nil {
				t.Errorf("%q: want error, got (%q, %q)", tt.raw, base, project)
			}
			continue
		}
		if err != nil || base != tt.wantBase || project != tt.wantProject {
			t.Errorf("%q: got (%q, %q, %v), want (%q, %q)", tt.raw, base, project, err, tt.wantBase, tt.wantProject)
		}
	}

	if p, base, project, err := ParseForgeURL("https://gitlab.example.com/g/r"); err != nil || p != "gitlab" || base != "https://gitlab.example.com" || project != "g/r" {
		t.Errorf("ParseForgeURL gitlab host: got (%q, %q, %q, %v)", p, base, project, err)
	}
	if _, _, _, err := ParseForgeURL("https://git.optcp.com/g/r"); err == nil {
		t.Errorf("ParseForgeURL neutral host: want error, heuristic cannot classify it")
	}
}
