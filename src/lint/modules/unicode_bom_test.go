package modules

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

func TestUnicode_LeadingBOMAllowed(t *testing.T) {
	path := writeTempFile(t, "scheme.colors", []byte("\uFEFF[Colors]\r\nTreeFontName=Tahoma\r\n"))

	findings, err := runUnicodeOnDisk(t, nil, "scheme.colors", path)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("a BOM as the file's first character must not be reported, got %+v", findings)
	}
}

func TestUnicode_BOMElsewhereStillCritical(t *testing.T) {
	cases := map[string]string{
		"later on the first line":  "[Colors]\uFEFF\n",
		"start of a later line":    "[Colors]\n\uFEFFTreeFontName=Tahoma\n",
		"second BOM after a first": "\uFEFF\uFEFF[Colors]\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeTempFile(t, "scheme.colors", []byte(content))

			findings, err := runUnicodeOnDisk(t, nil, "scheme.colors", path)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			if len(findings) != 1 || findings[0].Severity != lint.SeverityCritical {
				t.Fatalf("a U+FEFF that is not the file's first character must be one critical finding, got %+v", findings)
			}
		})
	}
}
