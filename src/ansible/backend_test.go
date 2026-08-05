package ansible

import (
	"encoding/base64"
	"strings"
	"testing"
)

// TestResolveKeyMaterial pins the credential forms: raw PEM wins, the _B64
// variant (the maskable single-line form) decodes, absence names both.
func TestResolveKeyMaterial(t *testing.T) {
	t.Setenv("T1_SSH_KEY", "raw-pem")
	if got, err := resolveKeyMaterial("T1"); err != nil || string(got) != "raw-pem" {
		t.Errorf("raw: %q %v", got, err)
	}

	t.Setenv("T2_SSH_KEY_B64", base64.StdEncoding.EncodeToString([]byte("decoded-pem\n"))+"\n")
	if got, err := resolveKeyMaterial("T2"); err != nil || string(got) != "decoded-pem\n" {
		t.Errorf("b64: %q %v", got, err)
	}

	t.Setenv("T3_SSH_KEY_B64", "not!!base64")
	if _, err := resolveKeyMaterial("T3"); err == nil || !strings.Contains(err.Error(), "decoding") {
		t.Errorf("bad b64 must error clearly: %v", err)
	}

	if _, err := resolveKeyMaterial("T4"); err == nil || !strings.Contains(err.Error(), "T4_SSH_KEY_B64") {
		t.Errorf("absence must name both forms: %v", err)
	}
}
