package modules

import "testing"

func TestIsCodeConstant(t *testing.T) {
	// IS a bounded numeric literal → objectively not a secret → classified out.
	for _, s := range []string{"0x68747541", "EBX=0x68747541", "  0xDEADBEEF  ", "ECX = 0x444D4163"} {
		if !isCodeConstant(s) {
			t.Errorf("%q should be classified as a code constant", s)
		}
	}
	// NOT a bare bounded literal → must stay flagged (the hard-classifier false-negative cases).
	for _, s := range []string{
		"AKIA0xDEADBEEFcafebabe1234",                   // real-key-shaped, merely contains 0x
		"0xabcdef0123456789abcdef0123456789abcdef0123", // long hex (hash / hex-encoded key) > 16 digits
		"df_aabbccdd11223344",                          // token-shaped, no 0x at all
		"ghp_0x1234567890abcdef",                       // provider-token-shaped containing 0x
	} {
		if isCodeConstant(s) {
			t.Errorf("%q must NOT be classified out — it could be a real secret", s)
		}
	}
}
