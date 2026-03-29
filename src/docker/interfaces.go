package docker

import "context"

// InventorySource resolves candidate hosts from an external source.
// Adapter boundary: no source-specific concepts (Ansible, etc.) leak past this.
type InventorySource interface {
	Name() string
	Resolve(ctx context.Context, selector TargetSelector) ([]HostTarget, error)
}

// SecretsProvider handles encryption/decryption for stack secret files.
// SOPS today, Vault/Infisical later.
type SecretsProvider interface {
	Name() string
	Decrypt(ctx context.Context, path string) ([]byte, error)
	Encrypt(ctx context.Context, path string, data []byte) error
	IsEncrypted(path string) bool
}

// HostTransport provides remote execution on a target host.
type HostTransport interface {
	Exec(ctx context.Context, cmd string, args ...string) (stdout, stderr []byte, err error)
	CopyTo(ctx context.Context, localPath, remotePath string) error
	Close() error
}
