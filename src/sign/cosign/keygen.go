package cosign

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/provision"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// KeyGen is a provision.KeyGenerator backed by `cosign generate-key-pair`. The
// Tier-0 key is generated with an EMPTY password — the signing state dir (an
// operator-chosen durable volume) is the protection boundary, not a passphrase.
// Tier-0 optimizes persistence + frictionless adoption, not maximum assurance;
// harden by climbing the custody ladder (SoftHSM → Vault → hardware).
type KeyGen struct {
	RootDir string
	Desired map[string]config.ToolConstraint
}

// GenerateKeyPair writes cosign.key/cosign.pub into dir. cosign emits exactly those
// filenames in its working directory, which match provision's canonical names.
func (g KeyGen) GenerateKeyPair(ctx context.Context, dir, keyFile, pubFile string) error {
	ver, _ := toolchain.ResolveVersion("cosign", "", g.Desired)
	res, err := provision.Resolve(ctx, g.RootDir, "cosign", ver, "signing key generation")
	if err != nil {
		return fmt.Errorf("resolve cosign: %w", err)
	}
	cmd := exec.CommandContext(ctx, res.Path, "generate-key-pair")
	cmd.Dir = dir
	cmd.Env = append(toolchain.CleanEnv(), "COSIGN_PASSWORD=")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cosign generate-key-pair: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}
