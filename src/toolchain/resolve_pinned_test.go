package toolchain

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/paths"
)

// TestResolvePinned_VerifiesAssertedDigest drives the real download-verify-install path:
// a matching asserted digest installs with pinned trust; a mismatch refuses to install.
func TestResolvePinned_VerifiesAssertedDigest(t *testing.T) {
	artifact := []byte("#!/bin/sh\necho fake\n")
	sum := sha256.Sum256(artifact)
	good := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(artifact)
	}))
	defer srv.Close()

	def := ToolDef{
		Name:        "faketool",
		BinaryName:  "faketool",
		Format:      "binary",
		DownloadURL: func(v string, tg Target) string { return srv.URL + "/faketool-" + v + tg.Libc },
	}

	t.Run("mismatch refuses to install", func(t *testing.T) {
		t.Setenv(paths.CacheRootEnv, t.TempDir())
		_, err := resolveWithDef(t.TempDir(), def, "1.0.0", "", strings.Repeat("0", 64))
		if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
			t.Fatalf("a wrong asserted digest must fail with checksum mismatch, got %v", err)
		}
	})

	t.Run("match installs with pinned trust", func(t *testing.T) {
		t.Setenv(paths.CacheRootEnv, t.TempDir())
		res, err := resolveWithDef(t.TempDir(), def, "1.0.0", "", good)
		if err != nil {
			t.Fatalf("matching digest must install: %v", err)
		}
		if res.Trust != TrustPinned {
			t.Errorf("trust = %q, want %q", res.Trust, TrustPinned)
		}
		if res.Path == "" {
			t.Error("installed binary path is empty")
		}
	})
}
