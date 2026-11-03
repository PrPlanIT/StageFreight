package docker

import (
	"strings"
	"testing"
)

// A bootstrapped builder of either driver is usable; otherwise the error carries its status and output.
func TestUsableBuilder(t *testing.T) {
	for _, info := range []BuilderInfo{
		{Name: "sf-builder", Driver: "remote", BootstrapOK: true},
		{Name: "sf-builder", Driver: "docker-container", BootstrapOK: true},
	} {
		if name, err := usableBuilder(info); err != nil || name != "sf-builder" {
			t.Errorf("%s: usableBuilder = %q, %v; want sf-builder", info.Driver, name, err)
		}
	}

	_, err := usableBuilder(BuilderInfo{Name: "sf-builder", Status: "buildkit TLS misconfigured",
		RawOutput: "missing /buildkit-certs/ca.pem"})
	if err == nil || !strings.Contains(err.Error(), "buildkit TLS misconfigured") || !strings.Contains(err.Error(), "/buildkit-certs/ca.pem") {
		t.Errorf("err = %v, want the status and the builder's output", err)
	}
	_, err = usableBuilder(BuilderInfo{Name: "sf-builder"})
	if err == nil || !strings.Contains(err.Error(), "bootstrap failed") {
		t.Errorf("err = %v, want a bootstrap failure", err)
	}
}
