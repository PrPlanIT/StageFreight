package build

import "testing"

func TestWithDescription(t *testing.T) {
	// A declared description is stamped as the OCI image.description label.
	got := WithDescription(map[string]string{"a": "b"}, "  A rootless osTicket image  ")
	if got[LabelDescription] != "A rootless osTicket image" {
		t.Errorf("description = %q, want trimmed value", got[LabelDescription])
	}
	if got["a"] != "b" {
		t.Errorf("existing labels must be preserved")
	}

	// No description → SF never invents the label (a Dockerfile's own value, if any, stands).
	if got := WithDescription(map[string]string{}, "   "); got[LabelDescription] != "" {
		t.Errorf("blank description must not stamp a label; got %q", got[LabelDescription])
	}

	// Nil-safe.
	if got := WithDescription(nil, "x"); got[LabelDescription] != "x" {
		t.Errorf("nil map + description should yield the label")
	}
}
