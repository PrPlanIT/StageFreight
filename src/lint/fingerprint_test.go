package lint

import "testing"

func TestFingerprint(t *testing.T) {
	base := Finding{File: "a.go", Module: "lineendings", RuleID: "trailing-whitespace", Anchor: "\tfoo", Line: 14, Message: "trailing whitespace"}

	moved := base
	moved.Line, moved.Column = 99, 7 // position differs
	if base.Fingerprint() != moved.Fingerprint() {
		t.Error("line/column must NOT affect fingerprint — position is not identity")
	}

	reworded := base
	reworded.Message = "trailing whitespace (tabs)" // presentation differs
	if base.Fingerprint() != reworded.Fingerprint() {
		t.Error("message must NOT affect fingerprint — presentation is not identity")
	}

	for _, diff := range []Finding{
		func() Finding { f := base; f.Anchor = "\tbar"; return f }(),
		func() Finding { f := base; f.File = "b.go"; return f }(),
		func() Finding { f := base; f.RuleID = "crlf"; return f }(),
		func() Finding { f := base; f.Module = "tabs"; return f }(),
	} {
		if base.Fingerprint() == diff.Fingerprint() {
			t.Errorf("fingerprint must change when an identity field changes: %+v", diff)
		}
	}
}
