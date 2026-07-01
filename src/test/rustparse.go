package test

import (
	"bufio"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// The libtest human output `cargo test` prints is STABLE across Rust versions and
// identical for every project — so parsing it is general (no nightly `--format
// json`, no required `nextest` binary). Each test binary (per crate × target: unit,
// integration, doc) emits a "Running …" line then a "test result: …" summary.
var (
	// "     Running unittests src/lib.rs (target/debug/deps/foo-1a2b3c4d)"
	reRunning = regexp.MustCompile(`^\s*Running\s+(.+?)\s+\(([^)]+)\)\s*$`)
	// "   Doc-tests foo"
	reDocTests = regexp.MustCompile(`^\s*Doc-tests\s+(\S+)`)
	// "test result: ok. 3 passed; 1 failed; 0 ignored; …"
	reResult = regexp.MustCompile(`^test result:\s+(ok|FAILED)\.\s+(\d+) passed;\s+(\d+) failed`)
	// "test tests::bar ... FAILED"
	reTestFail = regexp.MustCompile(`^test\s+(.+?)\s+\.\.\.\s+FAILED`)
	reFinished = regexp.MustCompile(`finished in ([0-9.]+)s`)
	// "---- tests::bar stdout ----" — start of a failed test's captured output.
	reFailHdr = regexp.MustCompile(`^----\s+(.+?)\s+stdout\s+----`)
)

// parseCargoTest parses the (merged stdout+stderr) stream of `cargo test` into
// per-test-binary results, streaming onDone as each binary's summary completes.
// Robust by design: unrecognized output is ignored and the caller falls back to the
// process exit code + captured log, so an unfamiliar layout still gates correctly.
func parseCargoTest(r io.Reader, onDone func(PackageResult)) []PackageResult {
	var out []PackageResult
	var pr *PackageResult
	var failed []string
	failOut := map[string]*strings.Builder{}
	var capturing string

	flush := func() {
		if pr == nil {
			return
		}
		for _, n := range failed {
			var o string
			if b := failOut[n]; b != nil {
				o = strings.TrimSpace(b.String())
			}
			pr.Failures = append(pr.Failures, TestFailure{Name: n, Output: o})
		}
		out = append(out, *pr)
		if onDone != nil {
			onDone(*pr)
		}
		pr, failed, capturing = nil, nil, ""
		failOut = map[string]*strings.Builder{}
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")

		switch {
		case reRunning.MatchString(line):
			m := reRunning.FindStringSubmatch(line)
			flush()
			crate := crateFromDepPath(m[2])
			pr = &PackageResult{ImportPath: crate, Rel: rustLabel(crate, m[1]), Coverage: -1}

		case reDocTests.MatchString(line):
			m := reDocTests.FindStringSubmatch(line)
			flush()
			pr = &PackageResult{ImportPath: m[1], Rel: m[1] + " · doc", Coverage: -1}

		case pr != nil && reResult.MatchString(line):
			m := reResult.FindStringSubmatch(line)
			passed, _ := strconv.Atoi(m[2])
			failedN, _ := strconv.Atoi(m[3])
			pr.Tests = passed + failedN
			switch {
			case m[1] == "FAILED" || failedN > 0:
				pr.Status = StatusFailed
			case passed == 0:
				pr.Status = StatusSkipped // binary ran but has no tests
			default:
				pr.Status = StatusPassed
			}
			if d := reFinished.FindStringSubmatch(line); d != nil {
				if v, err := strconv.ParseFloat(d[1], 64); err == nil {
					pr.Duration = secs(v)
				}
			}
			flush()

		case pr != nil && reFailHdr.MatchString(line):
			capturing = reFailHdr.FindStringSubmatch(line)[1]
			if failOut[capturing] == nil {
				failOut[capturing] = &strings.Builder{}
			}

		case pr != nil && strings.HasPrefix(strings.TrimSpace(line), "failures:"):
			capturing = "" // the trailing summary list — stop capturing panic output

		case pr != nil && reTestFail.MatchString(line):
			failed = append(failed, reTestFail.FindStringSubmatch(line)[1])
			capturing = ""

		case pr != nil && capturing != "":
			failOut[capturing].WriteString(line + "\n")
		}
	}
	flush() // stream ended without a trailing result line
	return out
}

// crateFromDepPath extracts the crate name from a cargo deps binary path, e.g.
// "target/debug/deps/dragonfly_core-1a2b3c4d" → "dragonfly_core".
func crateFromDepPath(p string) string {
	base := path.Base(p)
	if i := strings.LastIndexByte(base, '-'); i > 0 && isHexSuffix(base[i+1:]) {
		base = base[:i]
	}
	return base
}

func isHexSuffix(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// rustLabel builds a row label from the crate + the "Running" description. Unit
// tests are labelled by crate (e.g. "sfrusttest"); an integration binary is
// labelled by its target file ("tests/integration.rs" → "integration").
func rustLabel(crate, desc string) string {
	if strings.HasPrefix(desc, "unittests") {
		if crate != "" {
			return crate
		}
		return "unittests"
	}
	target := desc
	if i := strings.LastIndexByte(desc, ' '); i >= 0 {
		target = desc[i+1:]
	}
	return strings.TrimSuffix(path.Base(target), ".rs")
}
