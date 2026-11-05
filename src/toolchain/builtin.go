package toolchain

import (
	"sync"

	stagefreight "github.com/PrPlanIT/StageFreight"
)

var builtinLock = sync.OnceValues(func() (*Lock, error) {
	return parseLock(stagefreight.ToolchainsLock, "StageFreight's embedded toolchains.lock")
})

// BuiltinVersion is the version StageFreight runs for tool when the repository's lock does
// not resolve it: StageFreight's own locked version, kept current by its dependency update.
// "" when StageFreight does not lock tool.
func BuiltinVersion(tool string) string {
	l, err := builtinLock()
	if err != nil {
		return ""
	}
	return l.Resolved(tool)
}

// builtinDigest is the digest StageFreight's own lock records for tool at version.
func builtinDigest(tool, version, platform string) string {
	l, err := builtinLock()
	if err != nil {
		return ""
	}
	return l.Digest(tool, version, platform)
}
