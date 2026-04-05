package gitops

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/runtime"
)

// BuildKubeconfig creates an isolated kubeconfig for the target cluster.
// CA is resolved from environment: <PREFIX>_CA_FILE or <PREFIX>_CA_B64.
// OIDC token is resolved from STAGEFREIGHT_OIDC.
// All ephemeral files are registered for cleanup on rctx.Resolved.
func BuildKubeconfig(cfg config.ClusterConfig, rctx *runtime.RuntimeContext) error {
	prefix := envPrefix(cfg.Name)

	// Create isolated kubeconfig — never mutate ~/.kube/config.
	kubeconfigFile, err := os.CreateTemp("", "stagefreight-kubeconfig-*")
	if err != nil {
		return fmt.Errorf("creating kubeconfig tmpfile: %w", err)
	}
	if err := kubeconfigFile.Chmod(0600); err != nil {
		os.Remove(kubeconfigFile.Name())
		return fmt.Errorf("setting kubeconfig permissions: %w", err)
	}
	kubeconfigFile.Close()
	rctx.Resolved.KubeconfigPath = kubeconfigFile.Name()
	rctx.Resolved.AddCleanup(func() { os.Remove(kubeconfigFile.Name()) })

	os.Setenv("KUBECONFIG", kubeconfigFile.Name())

	// Resolve CA.
	caPath, err := resolveCA(prefix, rctx)
	if err != nil {
		return err
	}

	// Resolve auth — OIDC token or service account token.
	// OIDC preferred, then SA token fallback, then hard fail.
	token := os.Getenv("STAGEFREIGHT_OIDC")
	credName := "oidc"
	if token == "" {
		// Fallback: service account token (in-cluster or CI-injected).
		saToken := os.Getenv(prefix + "_TOKEN")
		if saToken == "" {
			// Try Kubernetes-mounted SA token.
			if data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token"); err == nil {
				saToken = string(data)
			}
		}
		if saToken != "" {
			token = saToken
			credName = "sa-token"
		}
	}
	if token == "" {
		return fmt.Errorf("no cluster auth: set STAGEFREIGHT_OIDC or %s_TOKEN", prefix)
	}

	fmt.Fprintf(os.Stderr, "prepare[flux]: auth method=%s\n", credName)

	// Build kubeconfig via kubectl.
	if err := kubectl("config", "set-cluster", cfg.Name,
		"--server="+cfg.Server,
		"--certificate-authority="+caPath,
	); err != nil {
		return fmt.Errorf("kubectl set-cluster: %w", err)
	}

	if err := kubectl("config", "set-credentials", credName,
		"--token="+token,
	); err != nil {
		return fmt.Errorf("kubectl set-credentials: %w", err)
	}

	if err := kubectl("config", "set-context", cfg.Name,
		"--cluster="+cfg.Name,
		"--user="+credName,
	); err != nil {
		return fmt.Errorf("kubectl set-context: %w", err)
	}

	if err := kubectl("config", "use-context", cfg.Name); err != nil {
		return fmt.Errorf("kubectl use-context: %w", err)
	}

	return nil
}

// resolveCA resolves the cluster CA from environment variables.
// Checks <PREFIX>_CA_FILE first, then <PREFIX>_CA_B64.
func resolveCA(prefix string, rctx *runtime.RuntimeContext) (string, error) {
	// Option 1: file path.
	fileVar := prefix + "_CA_FILE"
	if path := os.Getenv(fileVar); path != "" {
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("%s points to %q: %w", fileVar, path, err)
		}
		return path, nil
	}

	// Option 2: base64-encoded PEM.
	b64Var := prefix + "_CA_B64"
	if encoded := os.Getenv(b64Var); encoded != "" {
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return "", fmt.Errorf("decoding %s: %w", b64Var, err)
		}

		tmpFile, err := os.CreateTemp("", "stagefreight-ca-*")
		if err != nil {
			return "", fmt.Errorf("creating CA tmpfile: %w", err)
		}
		if err := tmpFile.Chmod(0600); err != nil {
			os.Remove(tmpFile.Name())
			return "", fmt.Errorf("setting CA tmpfile permissions: %w", err)
		}
		if _, err := tmpFile.Write(decoded); err != nil {
			os.Remove(tmpFile.Name())
			return "", fmt.Errorf("writing CA tmpfile: %w", err)
		}
		tmpFile.Close()

		rctx.Resolved.CAPath = tmpFile.Name()
		rctx.Resolved.AddCleanup(func() { os.Remove(tmpFile.Name()) })
		return tmpFile.Name(), nil
	}

	return "", fmt.Errorf("neither %s nor %s is set", fileVar, b64Var)
}

// envPrefix derives the environment variable prefix from a cluster name.
// Uppercased, hyphens replaced with underscores.
func envPrefix(name string) string {
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

// kubectl runs a kubectl command and returns any error.
func kubectl(args ...string) error {
	cmd := exec.Command("kubectl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
