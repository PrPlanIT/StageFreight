package ansible

import (
	"regexp"
	"strconv"
	"strings"
)

// HostRecap is one host's line from an ansible PLAY RECAP block.
type HostRecap struct {
	Host        string
	OK          int
	Changed     int
	Unreachable int
	Failed      int
	Skipped     int
	Rescued     int
	Ignored     int
}

// Converged reports whether the host completed the play without failures —
// the desired-state contract this subsystem exists to enforce.
func (h HostRecap) Converged() bool { return h.Failed == 0 && h.Unreachable == 0 }

// PlayResult is the structured outcome of executing one playbook.
type PlayResult struct {
	ID       string // playbook id from the library
	Path     string // repo-relative playbook path
	Check    bool   // true when run with --check --diff (plan mode)
	ExitCode int
	Hosts    []HostRecap
	Output   string // combined stdout/stderr (human stream, recap included)
}

// FailedHosts returns the hosts that failed or were unreachable.
func (p PlayResult) FailedHosts() []string {
	var out []string
	for _, h := range p.Hosts {
		if !h.Converged() {
			out = append(out, h.Host)
		}
	}
	return out
}

// Aggregate is the converge-set rollup that feeds the ansible.* facts.
type Aggregate struct {
	Total       int // unique hosts touched across the set
	Converged   int // hosts with zero failed/unreachable in every play
	Changed     int // hosts where at least one play changed something
	Unreachable int // hosts unreachable in at least one play
	Failed      int // hosts failed in at least one play
}

// AggregateResults rolls per-play recaps up to unique-host totals.
func AggregateResults(plays []PlayResult) Aggregate {
	type hostState struct{ changed, unreachable, failed bool }
	hosts := map[string]*hostState{}
	for _, p := range plays {
		for _, h := range p.Hosts {
			st, ok := hosts[h.Host]
			if !ok {
				st = &hostState{}
				hosts[h.Host] = st
			}
			st.changed = st.changed || h.Changed > 0
			st.unreachable = st.unreachable || h.Unreachable > 0
			st.failed = st.failed || h.Failed > 0
		}
	}
	var agg Aggregate
	agg.Total = len(hosts)
	for _, st := range hosts {
		if st.unreachable {
			agg.Unreachable++
		}
		if st.failed {
			agg.Failed++
		}
		if !st.unreachable && !st.failed {
			agg.Converged++
		}
		if st.changed {
			agg.Changed++
		}
	}
	return agg
}

// recapLineRe matches one PLAY RECAP host line. The recap block's key=value
// token format has been stable across ansible-core releases; unknown extra
// tokens are ignored rather than failing the parse.
var recapLineRe = regexp.MustCompile(`^(\S+)\s*:\s*((?:[a-z]+=\d+\s*)+)$`)

// ParseRecap extracts per-host counters from ansible-playbook output. Only
// lines after the "PLAY RECAP" marker are considered, so task output that
// happens to look counter-shaped can't pollute the result. An output with no
// recap returns nil — the caller treats that as a failed, unparseable run.
func ParseRecap(output string) []HostRecap {
	var recaps []HostRecap
	inRecap := false
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(stripANSI(line))
		if strings.HasPrefix(line, "PLAY RECAP") {
			inRecap = true
			continue
		}
		if !inRecap || line == "" {
			continue
		}
		m := recapLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		h := HostRecap{Host: m[1]}
		for _, tok := range strings.Fields(m[2]) {
			k, v, ok := strings.Cut(tok, "=")
			if !ok {
				continue
			}
			n, err := strconv.Atoi(v)
			if err != nil {
				continue
			}
			switch k {
			case "ok":
				h.OK = n
			case "changed":
				h.Changed = n
			case "unreachable":
				h.Unreachable = n
			case "failed":
				h.Failed = n
			case "skipped":
				h.Skipped = n
			case "rescued":
				h.Rescued = n
			case "ignored":
				h.Ignored = n
			}
		}
		recaps = append(recaps, h)
	}
	return recaps
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// stripANSI removes color escape sequences — ansible colorizes recap lines
// when it believes it has a TTY, and CI log capture preserves the codes.
func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }
