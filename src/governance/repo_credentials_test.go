package governance

import "testing"

// Reads and commits must share an identity. These credentials are known from the policy
// before any satellite is read, so planning can use them, not only committing.
func TestRepoCredentialsComeFromTheProfileBeforePlanning(t *testing.T) {
	gov := &GovernanceConfig{Profiles: ProfileList{
		{ID: "sites", Credentials: "SOFMERIGHT", Repos: ProfileCatalog{{At: "SoFMeRight/kai-hamilton.com"}, {At: "SoFMeRight/astralfocal.com"}}},
		{ID: "images", Repos: ProfileCatalog{{At: "HomeLabHD/frappe-suite"}}},
	}}
	got := gov.RepoCredentials()
	if got["SoFMeRight/kai-hamilton.com"] != "SOFMERIGHT" || got["SoFMeRight/astralfocal.com"] != "SOFMERIGHT" {
		t.Errorf("profile credentials: got %v", got)
	}
	if _, ok := got["HomeLabHD/frappe-suite"]; ok {
		t.Errorf("a profile without credentials uses the default: got %v", got)
	}
}
