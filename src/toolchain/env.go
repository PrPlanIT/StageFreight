package toolchain

import (
	"os"
	"path/filepath"
)

// CleanEnv returns a minimal execution environment for toolchain-resolved binaries.
// No implicit PATH. No host leakage. All tools are invoked by absolute path.
//
// Tools that need specific env vars (e.g. GRYPE_DB_CACHE_DIR, COSIGN_YES)
// append them to the returned slice at the call site.
func CleanEnv() []string {
	env := []string{
		"HOME=/tmp",
		"PATH=", // empty — all tools invoked by absolute path
	}

	// Forward proxy settings if present
	for _, key := range []string{
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
		"http_proxy", "https_proxy", "no_proxy",
	} {
		if val, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+val)
		}
	}

	return env
}

// RegistryAuthEnv returns DOCKER_CONFIG pointing at the caller's registry credentials
// (an explicit DOCKER_CONFIG, else ~/.docker when it holds a config.json), or nil.
// CleanEnv's HOME=/tmp would otherwise hide them.
func RegistryAuthEnv() []string {
	if dc, ok := os.LookupEnv("DOCKER_CONFIG"); ok && dc != "" {
		return []string{"DOCKER_CONFIG=" + dc}
	}
	if home := os.Getenv("HOME"); home != "" {
		cfgDir := filepath.Join(home, ".docker")
		if _, err := os.Stat(filepath.Join(cfgDir, "config.json")); err == nil {
			return []string{"DOCKER_CONFIG=" + cfgDir}
		}
	}
	return nil
}
