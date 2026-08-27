package docker

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// A build must never be able to take its runner down. Left uncapped, a build that
// exhausts RAM does not fail cleanly: with swap present the kernel prefers swapping to
// killing, so it thrashes indefinitely, starves sshd and the network stack of CPU, and
// the host goes unreachable until someone power-cycles it. The OOM killer never fires,
// because reclaim keeps "succeeding" — it just never finishes. That is a livelock, and
// it is strictly worse than a failed job.
//
// The cap is a RESERVE, not an estimate: StageFreight never guesses what a build needs
// (unknowable). It bounds the builder by what the machine can actually give it.
//
// That bound must come from MemAvailable, not MemTotal. A total-based cap looks
// deterministic but does not hold: the runner agent, the docker daemon, an external
// buildkitd and the OS are already resident, so "total minus a host reserve" hands the
// builder memory that is already spent — 3.8 GiB total, ~1.5 GiB in use, and a 3.0 GiB
// cap permits 4.5 GiB of demand on a 3.8 GiB machine. The host swaps anyway and the
// cap accomplishes nothing. Bounding by what is AVAILABLE, minus a slice kept back for
// the host, is what actually makes the invariant hold.
//
// The cost is that the limit reflects the machine's state when the builder is created,
// so a loaded runner yields a smaller cap. That is not flakiness to be engineered
// away — it is the truth about what the build can have. Failing a build early beats
// killing the machine, and a build that only fits on an idle host was never safe.
//
// memory-swap is pinned EQUAL to memory, which disables swap for the builder. That is
// the load-bearing half: capping RAM while leaving swap available just relocates the
// thrash. The tradeoff is deliberate — a build that only "succeeded" by swapping was
// already pathological, and failing it fast beats a twelve-minute livelock.

const (
	// hostReserveBytes is held back from what is available so the host keeps room to
	// breathe — sshd stays answerable, the runner agent keeps reporting, and the
	// kernel retains page cache. Withheld from AVAILABLE memory, so it is headroom on
	// top of whatever is already resident, not an allowance for it.
	hostReserveBytes = 768 << 20 // 768 MiB

	// minBuilderBytes is the floor below which capping is pointless — a builder with
	// less than this cannot do useful work, so a tiny host is left uncapped rather
	// than given a cap that guarantees failure.
	minBuilderBytes = 1 << 30 // 1 GiB
)

// BuilderMemoryLimit resolves the cgroup cap for the builder container as a docker
// size string, or "" when no cap should be applied. An explicit
// build_cache.builder.memory wins; otherwise it is what the machine can actually spare
// right now: MemAvailable minus the host reserve.
func BuilderMemoryLimit(cfg config.BuilderConfig) string {
	if m := strings.TrimSpace(cfg.Memory); m != "" {
		return m
	}
	avail := hostMemAvailableBytes()
	if avail <= 0 {
		return "" // cannot read the machine — do not invent a limit
	}
	grant := avail - hostReserveBytes
	if grant < minBuilderBytes {
		// Too little to give: capping here would fail every build. Leave it uncapped
		// and let the operator see an honestly under-provisioned runner.
		return ""
	}
	return fmt.Sprintf("%db", grant)
}

// hostMemAvailableBytes reads MemAvailable — memory the kernel believes it can hand
// out without swapping, which already accounts for everything resident and for
// reclaimable cache. MemTotal is deliberately NOT used: it describes the machine, not
// what is left of it, and bounding by it overcommits a busy runner.
func hostMemAvailableBytes() int64 {
	if runtime.GOOS != "linux" {
		return 0
	}
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "MemAvailable:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return kb * 1024
	}
	return 0
}

// memoryDriverOpts renders the buildx driver options that place the cap on the builder
// container. Both keys are required: memory alone still permits swap, which is the
// thrash this exists to prevent.
func memoryDriverOpts(limit string) []string {
	if limit == "" {
		return nil
	}
	return []string{
		"--driver-opt", "memory=" + limit,
		"--driver-opt", "memory-swap=" + limit,
	}
}

// builderMemorySuffix renders the cap for the builder line so an operator can SEE
// whether this runner is protected rather than assuming it. An uncapped builder says
// so — silence would read as "capped".
func builderMemorySuffix(info BuilderInfo) string {
	switch {
	case info.MemoryLimit != "":
		return " · mem " + humanLimit(info.MemoryLimit) + " (no swap)"
	case info.MemoryNote != "":
		return " · uncapped: " + info.MemoryNote
	default:
		return " · uncapped"
	}
}

// humanLimit renders a byte-count limit compactly; a limit the operator wrote
// (e.g. "3g") passes through unchanged.
func humanLimit(limit string) string {
	var b int64
	if _, err := fmt.Sscanf(limit, "%db", &b); err != nil || b <= 0 {
		return limit
	}
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.0fM", float64(b)/(1<<20))
	}
	return limit
}
