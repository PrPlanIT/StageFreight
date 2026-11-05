// Package stagefreight embeds StageFreight's own toolchain lock: the versions and digests
// a repository runs for a toolchain its own lock does not resolve.
package stagefreight

import _ "embed"

//go:embed .stagefreight/toolchains.lock
var ToolchainsLock []byte
