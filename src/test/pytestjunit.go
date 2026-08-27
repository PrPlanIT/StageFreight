package test

import (
	"encoding/xml"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// pytest's structured transport is JUnit XML (`--junitxml`), which is CORE pytest —
// no plugin, and stable across versions in a way its human output is not. That makes
// it the Python analogue of `go test -json`: parse the transport, never the prose.
//
// The unit projection: Go reports per PACKAGE, Rust per TEST BINARY, Python per TEST
// MODULE — a <testcase>'s classname is its dotted module path (tests.test_scraper),
// which is the natural grouping a developer already thinks in.

type junitSuites struct {
	XMLName xml.Name     `xml:"testsuites"`
	Suites  []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	XMLName xml.Name    `xml:"testsuite"`
	Name    string      `xml:"name,attr"`
	Time    string      `xml:"time,attr"`
	Cases   []junitCase `xml:"testcase"`
}

type junitCase struct {
	Classname string        `xml:"classname,attr"`
	Name      string        `xml:"name,attr"`
	File      string        `xml:"file,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitDetail  `xml:"failure"`
	Error     *junitDetail  `xml:"error"`
	Skipped   *junitSkipped `xml:"skipped"`
}

type junitDetail struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}

type junitSkipped struct {
	Message string `xml:"message,attr"`
}

// parsePytestJUnit reads a pytest --junitxml report into per-module results, firing
// onPkg as each module is completed (ordered by first appearance, matching the other
// parsers' streaming contract). A report with no testsuites yields no packages — the
// caller turns that plus a non-zero exit into a synthetic failure, exactly as the
// cargo path does for a compile error.
func parsePytestJUnit(path string, onPkg func(PackageResult)) []PackageResult {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc junitSuites
	if err := xml.Unmarshal(data, &doc); err != nil {
		// pytest emits a bare <testsuite> root in some configurations.
		var single junitSuite
		if err2 := xml.Unmarshal(data, &single); err2 != nil {
			return nil
		}
		doc.Suites = []junitSuite{single}
	}

	type acc struct {
		res   PackageResult
		order int
	}
	byModule := map[string]*acc{}
	order := 0

	for _, ts := range doc.Suites {
		for _, tc := range ts.Cases {
			mod := tc.Classname
			if mod == "" {
				mod = strings.TrimSuffix(tc.File, ".py")
				mod = strings.ReplaceAll(mod, "/", ".")
			}
			if mod == "" {
				mod = ts.Name
			}
			a := byModule[mod]
			if a == nil {
				a = &acc{res: PackageResult{
					ImportPath: mod,
					Rel:        moduleRel(mod, tc.File),
					Status:     StatusPassed,
					Coverage:   -1,
				}, order: order}
				byModule[mod] = a
				order++
			}
			a.res.Duration += parseSeconds(tc.Time)

			switch {
			case tc.Failure != nil || tc.Error != nil:
				d := tc.Failure
				if d == nil {
					d = tc.Error
				}
				a.res.Status = StatusFailed
				a.res.Failures = append(a.res.Failures, TestFailure{
					Name:   tc.Name,
					Output: strings.TrimSpace(nonEmpty(d.Body, d.Message)),
				})
			case tc.Skipped != nil:
				// A module of only-skips reports skipped; any real result outranks it.
				if len(a.res.Failures) == 0 && a.res.Status == StatusPassed && a.res.Tests == 0 {
					a.res.Status = StatusSkipped
				}
			default:
				if a.res.Status == StatusSkipped {
					a.res.Status = StatusPassed
				}
			}
			a.res.Tests++
		}
	}

	out := make([]PackageResult, 0, len(byModule))
	for _, a := range byModule {
		out = append(out, a.res)
	}
	sort.Slice(out, func(i, j int) bool {
		return byModule[out[i].ImportPath].order < byModule[out[j].ImportPath].order
	})
	if onPkg != nil {
		for _, p := range out {
			onPkg(p)
		}
	}
	return out
}

// moduleRel prefers the report's file path (repo-relative, what a developer opens)
// and falls back to the dotted module rendered as a path.
func moduleRel(mod, file string) string {
	if file != "" {
		return file
	}
	return strings.ReplaceAll(mod, ".", "/")
}

func parseSeconds(s string) time.Duration {
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s + "s")
	if err != nil {
		return 0
	}
	return d
}

func nonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// parsePytestCoverage extracts the total percentage from pytest-cov's terminal
// summary, whose final row is:
//
//	TOTAL                      1234    567    54%
//
// Mirrors parseGoCoverage: read the tool's own reported total rather than averaging
// per-file numbers, which would misweight small files.
func parsePytestCoverage(out string) (float64, bool) {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || !strings.EqualFold(f[0], "TOTAL") {
			continue
		}
		last := f[len(f)-1]
		if !strings.HasSuffix(last, "%") {
			continue
		}
		var pct float64
		if _, err := fmt.Sscanf(strings.TrimSuffix(last, "%"), "%f", &pct); err != nil {
			continue
		}
		return pct, true
	}
	return 0, false
}
