package runner

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// cgroupRoot is the process's cgroup mount; a variable so tests can point it at a fixture.
var cgroupRoot = "/sys/fs/cgroup"

// cgroupMemory reports the cgroup memory limit and the headroom under it, in MB.
// ok is false when no limit applies. Usage is the working set (usage minus inactive
// file cache), the figure the OOM killer acts on.
func cgroupMemory(root string) (limitMB, headroomMB int64, ok bool) {
	if raw, err := os.ReadFile(filepath.Join(root, "memory.max")); err == nil {
		limit, isLimited := parseCgroupLimit(string(raw))
		if !isLimited {
			return 0, 0, false
		}
		usage := readCgroupInt(filepath.Join(root, "memory.current"))
		inactive := readCgroupStat(filepath.Join(root, "memory.stat"), "inactive_file")
		return headroom(limit, usage, inactive)
	}
	v1 := filepath.Join(root, "memory")
	if raw, err := os.ReadFile(filepath.Join(v1, "memory.limit_in_bytes")); err == nil {
		limit, isLimited := parseCgroupLimit(string(raw))
		if !isLimited {
			return 0, 0, false
		}
		usage := readCgroupInt(filepath.Join(v1, "memory.usage_in_bytes"))
		inactive := readCgroupStat(filepath.Join(v1, "memory.stat"), "total_inactive_file")
		return headroom(limit, usage, inactive)
	}
	return 0, 0, false
}

// cgroupV1Unlimited is the smallest value treated as "no limit" in cgroup v1.
const cgroupV1Unlimited = int64(1) << 62

// parseCgroupLimit parses a memory limit file. isLimited is false for "max" (v2),
// the v1 near-MaxInt64 sentinel, or anything unparseable.
func parseCgroupLimit(raw string) (bytes int64, isLimited bool) {
	s := strings.TrimSpace(raw)
	if s == "" || s == "max" {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v <= 0 || v >= cgroupV1Unlimited {
		return 0, false
	}
	return v, true
}

func headroom(limit, usage, inactive int64) (limitMB, headroomMB int64, ok bool) {
	workingSet := usage - inactive
	if workingSet < 0 {
		workingSet = 0
	}
	free := limit - workingSet
	if free < 0 {
		free = 0
	}
	return limit / (1024 * 1024), free / (1024 * 1024), true
}

// readCgroupInt reads a single-integer cgroup file, 0 when absent or unparseable.
func readCgroupInt(path string) int64 {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// readCgroupStat reads one "key value" line from a memory.stat file, 0 when absent.
func readCgroupStat(path, key string) int64 {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == key {
			v, err := strconv.ParseInt(fields[1], 10, 64)
			if err == nil {
				return v
			}
		}
	}
	return 0
}

// MemoryLimitMB is this process's cgroup memory limit in MB, 0 when none applies.
func MemoryLimitMB() int64 {
	if limit, _, ok := cgroupMemory(cgroupRoot); ok {
		return limit
	}
	return 0
}

// OOMKills is the cgroup's OOM-kill count; a rise across a child's run means it died of
// the memory limit. ok is false when the counter is unavailable.
func OOMKills() (n int64, ok bool) {
	for _, p := range []string{
		filepath.Join(cgroupRoot, "memory.events"),
		filepath.Join(cgroupRoot, "memory", "memory.oom_control"),
	} {
		if _, err := os.Stat(p); err == nil {
			return readCgroupStat(p, "oom_kill"), true
		}
	}
	return 0, false
}
