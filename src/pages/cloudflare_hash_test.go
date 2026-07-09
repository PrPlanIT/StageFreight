package pages

import "testing"

// Conformance vectors taken verbatim from wrangler's own test suite
// (cloudflare/workers-sdk, packages/wrangler/src/__tests__/pages/deploy.test.ts):
// content "foobar" hashes to different values per extension because the extension is
// folded into the hash input. If these pass, our port is byte-identical to wrangler's
// hashFile — which is the whole point of porting the normative source rather than
// guessing the algorithm.
func TestCloudflarePagesV1Hasher_WranglerVectors(t *testing.T) {
	h := cloudflarePagesV1Hasher{}
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"logo.png", "foobar", "2082190357cfd3617ccfe04f340c6247"},
		{"logo.txt", "foobar", "1a98fb08af91aca4a7df1764a2c4ddb0"},
	}
	for _, c := range cases {
		if got := h.Hash(c.name, []byte(c.content)); got != c.want {
			t.Errorf("Hash(%q, %q) = %q, want %q (wrangler vector)", c.name, c.content, got, c.want)
		}
	}
	// The same content with a different extension must produce a different hash.
	if h.Hash("a.png", []byte("foobar")) == h.Hash("a.txt", []byte("foobar")) {
		t.Error("extension must affect the hash")
	}
}
