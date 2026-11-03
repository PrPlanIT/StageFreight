package runner

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const mib = int64(1024 * 1024)

// writeCgroup lays out a fake cgroup tree: files maps a relative path to content.
func writeCgroup(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func bytesOf(mb int64) string { return strconv.FormatInt(mb*mib, 10) }

// Limited and unlimited containers, on both cgroup versions.
func TestCgroupMemory(t *testing.T) {
	cases := []struct {
		name         string
		files        map[string]string
		wantOK       bool
		wantLimit    int64
		wantHeadroom int64
	}{
		{
			name: "k8s job pod, cgroup v2: 2Gi limit, 1.5Gi used, 512Mi of it inactive cache",
			files: map[string]string{
				"memory.max":     bytesOf(2048),
				"memory.current": bytesOf(1536),
				"memory.stat":    "anon 1\ninactive_file " + bytesOf(512) + "\nactive_file 1\n",
			},
			wantOK: true, wantLimit: 2048, wantHeadroom: 1024,
		},
		{
			name: "k8s job pod, cgroup v1: 2Gi limit, 1.75Gi used, no inactive cache",
			files: map[string]string{
				"memory/memory.limit_in_bytes": bytesOf(2048),
				"memory/memory.usage_in_bytes": bytesOf(1792),
				"memory/memory.stat":           "total_inactive_file 0\n",
			},
			wantOK: true, wantLimit: 2048, wantHeadroom: 256,
		},
		{
			name: "over the limit reports zero headroom, never negative",
			files: map[string]string{
				"memory.max":     bytesOf(1024),
				"memory.current": bytesOf(1100),
			},
			wantOK: true, wantLimit: 1024, wantHeadroom: 0,
		},
		{
			name:   "docker container without --memory, cgroup v2: unlimited",
			files:  map[string]string{"memory.max": "max\n", "memory.current": bytesOf(300)},
			wantOK: false,
		},
		{
			name:   "docker container without --memory, cgroup v1: near-MaxInt64 sentinel",
			files:  map[string]string{"memory/memory.limit_in_bytes": "9223372036854771712\n"},
			wantOK: false,
		},
		{
			name:   "no memory controller at all",
			files:  map[string]string{"cgroup.procs": "1\n"},
			wantOK: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			limit, headroom, ok := cgroupMemory(writeCgroup(t, c.files))
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if !ok {
				return
			}
			if limit != c.wantLimit || headroom != c.wantHeadroom {
				t.Fatalf("limit/headroom = %d/%d MB, want %d/%d MB", limit, headroom, c.wantLimit, c.wantHeadroom)
			}
		})
	}
}

// A limited container's facts report the headroom under its limit, not the node's memory.
func TestCollectFacts_MemoryUnderJobPodLimit(t *testing.T) {
	old := cgroupRoot
	t.Cleanup(func() { cgroupRoot = old })

	cgroupRoot = writeCgroup(t, map[string]string{
		"memory.max":     bytesOf(2048),
		"memory.current": bytesOf(1900),
	})
	facts := CollectFacts(t.TempDir())
	if facts.MemLimitMB != 2048 {
		t.Errorf("MemLimitMB = %d, want 2048", facts.MemLimitMB)
	}
	if facts.MemAvailableMB != 148 {
		t.Errorf("MemAvailableMB = %d, want 148 (limit minus working set)", facts.MemAvailableMB)
	}
	findings, grade := EvaluateHealth(facts, Options{})
	if grade != Degraded {
		t.Errorf("grade = %q, want degraded (148 MB < 512 MB warn)", grade)
	}
	for _, f := range findings {
		if f.ID == "memory_low" && !strings.Contains(f.Detail, "of a 2.0 GB limit") {
			t.Errorf("memory_low detail %q should name the 2.0 GB limit", f.Detail)
		}
	}
}

// An unlimited container keeps the node's MemAvailable and reports no limit.
func TestCollectFacts_MemoryUnlimitedContainer(t *testing.T) {
	old := cgroupRoot
	t.Cleanup(func() { cgroupRoot = old })

	cgroupRoot = writeCgroup(t, map[string]string{"memory.max": "max\n"})
	facts := CollectFacts(t.TempDir())
	if facts.MemLimitMB != -1 {
		t.Errorf("MemLimitMB = %d, want -1 (unlimited)", facts.MemLimitMB)
	}
	if _, err := os.Stat("/proc/meminfo"); err == nil && facts.MemAvailableMB < 0 {
		t.Errorf("MemAvailableMB = %d, want the node's MemAvailable", facts.MemAvailableMB)
	}
}

func TestExecutionReport_HardFailures(t *testing.T) {
	r := ExecutionReport{Findings: []Finding{
		{ID: "workdir_readable", Status: "ok", Severity: "hard_fail"},
		{ID: "docker_absent", Status: "fail", Detail: "docker socket not available", Severity: "hard_fail"},
		{ID: "disk_critical", Status: "fail", Detail: "100 MB free — below 512 MB minimum", Severity: "hard_fail"},
		{ID: "tmp_low", Status: "warn", Detail: "low", Severity: "warn"},
	}}
	want := "docker_absent: docker socket not available; disk_critical: 100 MB free — below 512 MB minimum"
	if got := r.HardFailures(); got != want {
		t.Fatalf("HardFailures() = %q, want %q", got, want)
	}
	if got := (ExecutionReport{}).HardFailures(); got != "" {
		t.Fatalf("no findings: got %q, want empty", got)
	}
}

// The OOM-kill counter and the limit, on both cgroup versions and with no controller.
func TestOOMKillsAndMemoryLimit(t *testing.T) {
	old := cgroupRoot
	t.Cleanup(func() { cgroupRoot = old })

	cgroupRoot = writeCgroup(t, map[string]string{
		"memory.max":    bytesOf(2048),
		"memory.events": "low 0\nhigh 0\nmax 12\noom 2\noom_kill 2\n",
	})
	if n, ok := OOMKills(); !ok || n != 2 {
		t.Errorf("v2 OOMKills = %d,%v, want 2,true", n, ok)
	}
	if mb := MemoryLimitMB(); mb != 2048 {
		t.Errorf("v2 MemoryLimitMB = %d, want 2048", mb)
	}

	cgroupRoot = writeCgroup(t, map[string]string{
		"memory/memory.limit_in_bytes": bytesOf(1024),
		"memory/memory.oom_control":    "oom_kill_disable 0\nunder_oom 0\noom_kill 5\n",
	})
	if n, ok := OOMKills(); !ok || n != 5 {
		t.Errorf("v1 OOMKills = %d,%v, want 5,true", n, ok)
	}
	if mb := MemoryLimitMB(); mb != 1024 {
		t.Errorf("v1 MemoryLimitMB = %d, want 1024", mb)
	}

	cgroupRoot = writeCgroup(t, map[string]string{"cgroup.procs": "1\n"})
	if _, ok := OOMKills(); ok {
		t.Error("no memory controller: OOMKills must report unavailable")
	}
	if mb := MemoryLimitMB(); mb != 0 {
		t.Errorf("no limit: MemoryLimitMB = %d, want 0", mb)
	}
}
