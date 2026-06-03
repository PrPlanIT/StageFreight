package gitops

import (
	"strings"
	"testing"
)

// TestKubectlEnvForwardsKubeconfig pins the fix for the reconcile localhost:8080
// regression: kubectl's env must carry KUBECONFIG so its `kubectl config` writes
// land in the same isolated kubeconfig flux later reads. CleanEnv strips it; if
// this forwarding regresses, kubectl writes to /tmp/.kube/config while flux reads
// an empty temp file and falls back to http://localhost:8080.
func TestKubectlEnvForwardsKubeconfig(t *testing.T) {
	t.Setenv("KUBECONFIG", "/tmp/stagefreight-kubeconfig-xyz")
	env := kubectlEnv()

	var got string
	for _, kv := range env {
		if strings.HasPrefix(kv, "KUBECONFIG=") {
			got = strings.TrimPrefix(kv, "KUBECONFIG=")
		}
	}
	if got != "/tmp/stagefreight-kubeconfig-xyz" {
		t.Fatalf("kubectlEnv did not forward KUBECONFIG; got %q, env=%v", got, env)
	}
}

// TestKubectlEnvOmitsEmptyKubeconfig: no KUBECONFIG set → no empty assignment
// leaks into the env (which would point kubectl at "", a different breakage).
func TestKubectlEnvOmitsEmptyKubeconfig(t *testing.T) {
	t.Setenv("KUBECONFIG", "")
	for _, kv := range kubectlEnv() {
		if strings.HasPrefix(kv, "KUBECONFIG=") {
			t.Fatalf("kubectlEnv emitted a KUBECONFIG entry when none was set: %q", kv)
		}
	}
}
