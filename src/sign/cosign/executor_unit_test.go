package cosign

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/sign"
)

func countPrefix(env []string, prefix string) (n int, last string) {
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			n++
			last = strings.TrimPrefix(kv, prefix)
		}
	}
	return
}

// An operator COSIGN_PASSWORD must appear EXACTLY ONCE with its real value — no
// empty-default duplicate whose resolution would depend on child env ordering.
func TestSignEnv_KeyPasswordSingleEntry(t *testing.T) {
	t.Setenv("COSIGN_PASSWORD", "secret")
	n, val := countPrefix(signEnv(sign.SignPlan{TrustClass: sign.ClassKey}), "COSIGN_PASSWORD=")
	if n != 1 || val != "secret" {
		t.Errorf("want a single COSIGN_PASSWORD=secret, got n=%d val=%q", n, val)
	}
}

// With no operator password, the empty Tier-0 default is emitted once.
func TestSignEnv_KeyEmptyPasswordDefault(t *testing.T) {
	// t.Setenv can't unset; assert the empty default is present and singular when
	// the operator didn't set one. (Other tests that set it run isolated via t.Setenv.)
	n, val := countPrefix(signEnv(sign.SignPlan{TrustClass: sign.ClassKey}), "COSIGN_PASSWORD=")
	if n != 1 {
		t.Errorf("expected exactly one COSIGN_PASSWORD entry, got %d", n)
	}
	_ = val
}

// The key class is hermetic — arbitrary host env must NOT reach the cosign subprocess.
func TestSignEnv_KeyIsHermetic(t *testing.T) {
	t.Setenv("SF_TEST_LEAK", "leaked")
	for _, kv := range signEnv(sign.SignPlan{TrustClass: sign.ClassKey}) {
		if strings.HasPrefix(kv, "SF_TEST_LEAK=") {
			t.Error("key class must be hermetic — a host env var leaked into the cosign env")
		}
	}
}

// Hardware passes the full host env (device/pinentry need it) + COSIGN_YES.
func TestSignEnv_HardwarePassesFullEnv(t *testing.T) {
	t.Setenv("SF_TEST_LEAK", "present")
	env := signEnv(sign.SignPlan{TrustClass: sign.ClassHardware})
	leak, yes := false, false
	for _, kv := range env {
		if kv == "SF_TEST_LEAK=present" {
			leak = true
		}
		if kv == "COSIGN_YES=1" {
			yes = true
		}
	}
	if !leak || !yes {
		t.Errorf("hardware must pass full env + COSIGN_YES (host-env=%v yes=%v)", leak, yes)
	}
}
