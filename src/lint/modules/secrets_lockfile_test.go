package modules

import "testing"

func TestLockfileIntegrityLineSuppression(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{`checksum = "8c0c0c0c0c..."`, true},               // Cargo.lock
		{`  "integrity": "sha512-AAAABBBBCCCC..."`, true},  // package-lock.json
		{`  integrity sha512-DDDDEEEEFFFF...`, true},       // yarn.lock
		{`golang.org/x/net v0.1.0 h1:GGGGHHHHIIII=`, true}, // go.sum
		{`"resolved": "https://registry/x.tgz"`, false},    // URL — keep (creds can hide here)
		{`let api_key = "AKIAIOSFODNN7EXAMPLE";`, false},   // a real-looking secret
		{`name = "openssl"`, false},                        // ordinary lock line
	}
	for _, c := range cases {
		if got := isLockfileIntegrityLine(c.line); got != c.want {
			t.Errorf("isLockfileIntegrityLine(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}
