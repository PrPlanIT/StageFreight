package paths

import "os"

// GitTrustEnv is the environment that lets a git started inside StageFreight's tool
// environment (cargo build scripts, tests, hooks) read the CI checkout it was handed. A
// runner that clones as one user and runs the job as another (GitLab's docker executor:
// root checkout, uid 1000 job) leaves every real git refusing it as "dubious ownership",
// and a build script that asks git for the commit silently falls back. StageFreight
// already executes that checkout's code; trusting it for git widens nothing. Only
// SF_CI_WORKSPACE is trusted, through git's environment config, so no file is written.
// Outside CI it is empty: a local checkout belongs to the user running it.
func GitTrustEnv() []string {
	dir := os.Getenv("SF_CI_WORKSPACE")
	if dir == "" {
		return nil
	}
	return []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=safe.directory",
		"GIT_CONFIG_VALUE_0=" + dir,
	}
}
