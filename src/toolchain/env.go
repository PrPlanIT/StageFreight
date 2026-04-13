package toolchain

import "os"

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
