package cosign

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
)

// WriteRegistryAuth writes a DOCKER_CONFIG dir with one registry's credentials, for
// sign.SignOptions.RegistryAuthDir; cleanup removes it. Docker Hub is keyed as
// https://index.docker.io/v1/, as cosign's keychain looks it up.
func WriteRegistryAuth(host, username, password string) (dir string, cleanup func(), err error) {
	reg, err := name.NewRegistry(host)
	if err != nil {
		return "", func() {}, fmt.Errorf("registry auth: parsing host %q: %w", host, err)
	}
	key := reg.RegistryStr()
	if key == name.DefaultRegistry {
		key = authn.DefaultAuthKey
	}
	dir, err = os.MkdirTemp("", "sf-registry-auth-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("registry auth: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(dir) }

	type entry struct {
		Auth string `json:"auth"`
	}
	cfg := struct {
		Auths map[string]entry `json:"auths"`
	}{Auths: map[string]entry{
		key: {Auth: base64.StdEncoding.EncodeToString([]byte(username + ":" + password))},
	}}
	data, err := json.Marshal(cfg)
	if err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("registry auth: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("registry auth: %w", err)
	}
	return dir, cleanup, nil
}
