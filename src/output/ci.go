package output

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

// CI environment detection.

func IsCI() bool {
	return os.Getenv("CI") == "true"
}

func IsGitLabCI() bool {
	return os.Getenv("GITLAB_CI") == "true"
}

// GitLab collapsible section helpers.

func SectionStart(w io.Writer, id, name string) {
	if !IsGitLabCI() {
		return
	}
	ts := time.Now().Unix()
	fmt.Fprintf(w, "\033[0Ksection_start:%d:%s\r\033[0K%s\n", ts, id, name)
}

func SectionEnd(w io.Writer, id string) {
	if !IsGitLabCI() {
		return
	}
	ts := time.Now().Unix()
	fmt.Fprintf(w, "\033[0Ksection_end:%d:%s\r\033[0K\n", ts, id)
}

// SectionStartCollapsed starts a section that is collapsed by default.
func SectionStartCollapsed(w io.Writer, id, name string) {
	if !IsGitLabCI() {
		return
	}
	ts := time.Now().Unix()
	fmt.Fprintf(w, "\033[0Ksection_start:%d:%s[collapsed=true]\r\033[0K%s\n", ts, id, name)
}

// JUnit XML types for GitLab test reporting.

type JUnitTestSuites struct {
	XMLName xml.Name         `xml:"testsuites"`
	Name    string           `xml:"name,attr"`
	Tests   int              `xml:"tests,attr"`
	Failures int             `xml:"failures,attr"`
	Time    string           `xml:"time,attr"`
	Suites  []JUnitTestSuite `xml:"testsuite"`
}

type JUnitTestSuite struct {
	Name     string          `xml:"name,attr"`
	Tests    int             `xml:"tests,attr"`
	Failures int             `xml:"failures,attr"`
	Time     string          `xml:"time,attr"`
	Cases    []JUnitTestCase `xml:"testcase"`
}

type JUnitTestCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *JUnitFailure `xml:"failure,omitempty"`
}

type JUnitFailure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Body    string `xml:",chardata"`
}

// WriteLintJUnit writes lint findings as JUnit XML for GitLab test reporting.
// Each lint module becomes a test suite, each scanned file becomes a test case.
func WriteLintJUnit(dir string, findings []lint.Finding, files []lint.FileInfo, modules []string, elapsed time.Duration) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating report dir: %w", err)
	}

	// Group findings by module → file
	byModule := make(map[string]map[string][]lint.Finding)
	for _, m := range modules {
		byModule[m] = make(map[string][]lint.Finding)
	}
	for _, f := range findings {
		if _, ok := byModule[f.Module]; !ok {
			byModule[f.Module] = make(map[string][]lint.Finding)
		}
		byModule[f.Module][f.File] = append(byModule[f.Module][f.File], f)
	}

	// Build file set for test cases
	fileSet := make(map[string]bool, len(files))
	for _, f := range files {
		fileSet[f.Path] = true
	}

	totalTests := 0
	totalFailures := 0
	var suites []JUnitTestSuite

	for _, mod := range modules {
		modFindings := byModule[mod]
		suite := JUnitTestSuite{
			Name: "stagefreight/lint/" + mod,
			Time: fmt.Sprintf("%.3f", elapsed.Seconds()/float64(len(modules))),
		}

		// Create a test case for each file scanned
		for _, f := range files {
			tc := JUnitTestCase{
				Name:      f.Path,
				Classname: "stagefreight.lint." + mod,
				Time:      "0.000",
			}

			if ff, ok := modFindings[f.Path]; ok && len(ff) > 0 {
				// Find worst severity
				worst := lint.SeverityInfo
				var lines []string
				for _, finding := range ff {
					if finding.Severity > worst {
						worst = finding.Severity
					}
					loc := fmt.Sprintf("%d", finding.Line)
					if finding.Column > 0 {
						loc = fmt.Sprintf("%d:%d", finding.Line, finding.Column)
					}
					lines = append(lines, fmt.Sprintf("  %s [%s] %s", loc, finding.Severity, finding.Message))
				}

				// Only critical findings are failures; warnings are not
				if worst >= lint.SeverityCritical {
					tc.Failure = &JUnitFailure{
						Message: fmt.Sprintf("%d finding(s) in %s", len(ff), f.Path),
						Type:    worst.String(),
						Body:    strings.Join(lines, "\n"),
					}
					suite.Failures++
					totalFailures++
				}
			}

			suite.Cases = append(suite.Cases, tc)
			suite.Tests++
			totalTests++
		}

		suites = append(suites, suite)
	}

	root := JUnitTestSuites{
		Name:     "stagefreight-lint",
		Tests:    totalTests,
		Failures: totalFailures,
		Time:     fmt.Sprintf("%.3f", elapsed.Seconds()),
		Suites:   suites,
	}

	path := filepath.Join(dir, "lint.xml")
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	defer f.Close()

	f.WriteString(xml.Header)
	enc := xml.NewEncoder(f)
	enc.Indent("", "  ")
	if err := enc.Encode(root); err != nil {
		return fmt.Errorf("encoding junit xml: %w", err)
	}
	f.WriteString("\n")

	return nil
}

// CIHeader prints a compact pipeline context block at the start of a CI run.
func CIHeader(w io.Writer) {
	if !IsCI() {
		return
	}
	parts := []string{}
	if tag := os.Getenv("CI_COMMIT_TAG"); tag != "" {
		parts = append(parts, fmt.Sprintf("tag=%s", tag))
	}
	if sha := os.Getenv("CI_COMMIT_SHORT_SHA"); sha != "" {
		parts = append(parts, fmt.Sprintf("sha=%s", sha))
	} else if sha := os.Getenv("CI_COMMIT_SHA"); sha != "" && len(sha) >= 8 {
		parts = append(parts, fmt.Sprintf("sha=%s", sha[:8]))
	}
	if pipe := os.Getenv("CI_PIPELINE_ID"); pipe != "" {
		parts = append(parts, fmt.Sprintf("pipeline=%s", pipe))
	}
	if runner := os.Getenv("CI_RUNNER_DESCRIPTION"); runner != "" {
		parts = append(parts, fmt.Sprintf("runner=%s", runner))
	}
	if len(parts) > 0 {
		fmt.Fprintf(w, "  ci: %s\n", strings.Join(parts, "  "))
	}
}

// PhaseResult prints a compact single-line phase summary.
func PhaseResult(w io.Writer, name, status, detail string, elapsed time.Duration) {
	icon := "\033[32m✓\033[0m"
	if status == "failed" {
		icon = "\033[31m✗\033[0m"
	} else if status == "skipped" {
		icon = "\033[33m⊘\033[0m"
	}
	fmt.Fprintf(w, "  %-10s %s  %-50s (%s)\n", name, icon, detail, elapsed.Round(time.Millisecond))
}
