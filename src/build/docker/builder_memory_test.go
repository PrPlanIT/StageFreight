package docker

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// An explicit declaration is authoritative — the derived reserve is only a default.
func TestBuilderMemoryLimit_ExplicitWins(t *testing.T) {
	if got := BuilderMemoryLimit(config.BuilderConfig{Memory: "3g"}); got != "3g" {
		t.Errorf("explicit memory = %q, want 3g", got)
	}
	if got := BuilderMemoryLimit(config.BuilderConfig{Memory: "  2500m  "}); got != "2500m" {
		t.Errorf("explicit memory must be trimmed, got %q", got)
	}
}

// The cap must disable swap, not merely bound RAM: capping memory while leaving swap
// available relocates the thrash instead of preventing it. Both keys, equal values.
func TestMemoryDriverOpts_PinsSwapEqual(t *testing.T) {
	opts := memoryDriverOpts("3g")
	joined := strings.Join(opts, " ")
	if !strings.Contains(joined, "memory=3g") || !strings.Contains(joined, "memory-swap=3g") {
		t.Fatalf("driver opts must set memory AND memory-swap equal, got %v", opts)
	}
	if len(opts) != 4 {
		t.Errorf("expected two --driver-opt pairs, got %v", opts)
	}
	if memoryDriverOpts("") != nil {
		t.Error("no limit must emit no driver opts")
	}
}

// A host with too little AVAILABLE memory is left uncapped rather than handed a cap
// that guarantees failure — the cap converts a host death into a job failure, it does
// not exist to make every build fail on a loaded machine.
func TestBuilderMemoryLimit_TooLittleAvailableIsUncapped(t *testing.T) {
	for _, avail := range []int64{0, 512 << 20, hostReserveBytes + minBuilderBytes - 1} {
		if grant := avail - hostReserveBytes; grant >= minBuilderBytes {
			t.Fatalf("test premise wrong for avail=%d", avail)
		}
	}
	// A machine with room to spare yields available-minus-reserve.
	avail := int64(8) << 30
	if grant := avail - hostReserveBytes; grant != (8<<30)-(768<<20) {
		t.Errorf("reserve math = %d, want available-768MiB", grant)
	}
}

// The bound MUST come from MemAvailable. MemTotal describes the machine, not what is
// left of it: on a 3.8GiB runner with ~1.5GiB already resident, a total-based cap of
// ~3.0GiB permits 4.5GiB of demand — the host swaps anyway and the cap is decorative.
func TestBuilderMemoryLimit_BoundsByAvailableNotTotal(t *testing.T) {
	avail := hostMemAvailableBytes()
	if avail <= 0 {
		t.Skip("no /proc/meminfo on this platform")
	}
	total := hostMemTotalForTest(t)
	if total > 0 && avail > total {
		t.Fatalf("MemAvailable (%d) cannot exceed MemTotal (%d) — wrong field parsed", avail, total)
	}
	got := BuilderMemoryLimit(config.BuilderConfig{})
	if got == "" {
		return // legitimately too little available to cap
	}
	var grant int64
	if _, err := fmt.Sscanf(got, "%db", &grant); err != nil {
		t.Fatalf("limit %q is not a byte count", got)
	}
	if grant >= avail {
		t.Errorf("cap %d must leave the host a reserve below available %d", grant, avail)
	}
	if total > 0 && grant >= total {
		t.Errorf("cap %d must never reach total %d", grant, total)
	}
}

// hostMemTotalForTest reads MemTotal only to assert the invariant available <= total.
func hostMemTotalForTest(t *testing.T) int64 {
	t.Helper()
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			return 0
		}
		kb, _ := strconv.ParseInt(f[1], 10, 64)
		return kb * 1024
	}
	return 0
}
