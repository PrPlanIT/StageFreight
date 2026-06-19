package cmd

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/sign"
)

// envForClass selects the hardware transport: a resolved PKCS#11 witness (PIV/HSM) is
// preferred, and FIDO2 --sk is the fallback ONLY when no PKCS#11 witness resolves — so
// the `hardware` class means "a non-exportable key in a signing device", transport is
// an implementation detail.
func TestEnvForClass_PKCS11PreferredOverFIDO2(t *testing.T) {
	t.Setenv("SF_PKCS11_YK", "pkcs11:id=%02?module-path=/usr/lib/libykcs11.so")

	// pkcs11 ref present + resolvable → PKCS#11 witness, NO FIDO2 fallback.
	env := envForClass(sign.SignPlan{TrustClass: sign.ClassHardware, PKCS11Ref: "yk"})
	if len(env.PKCS11) != 1 || len(env.FIDO2) != 0 {
		t.Fatalf("a resolvable pkcs11 ref must select PKCS#11 and skip the FIDO2 fallback: pkcs11=%+v fido2=%+v", env.PKCS11, env.FIDO2)
	}

	// no pkcs11 ref → FIDO2 --sk fallback (preserves the prior behavior).
	fb := envForClass(sign.SignPlan{TrustClass: sign.ClassHardware})
	if len(fb.FIDO2) != 1 || len(fb.PKCS11) != 0 {
		t.Fatalf("no pkcs11 → FIDO2 fallback: fido2=%+v pkcs11=%+v", fb.FIDO2, fb.PKCS11)
	}
}
