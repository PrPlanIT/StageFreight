package docker

import "testing"

// The signImages join key must converge a configured registry URL (which may carry
// a scheme, case, or trailing slash) onto the normalized buildx obs.Host — otherwise
// the signing profile is silently dropped (the P0 bypass). Both sides go through
// normalizeRegistryHost; this proves it converges the cases that bit us.
func TestSignImages_JoinKeyConverges(t *testing.T) {
	obs := normalizeRegistryHost("docker.io") // what buildx records
	for _, configured := range []string{
		"docker.io",
		"https://docker.io",
		"http://docker.io/",
		"Docker.IO",
		"https://Docker.IO/",
	} {
		if got := normalizeRegistryHost(configured); got != obs {
			t.Errorf("configured %q → %q, does not match obs.Host %q (signing would be dropped)", configured, got, obs)
		}
	}
}
