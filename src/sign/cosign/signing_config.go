package cosign

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"

	"github.com/PrPlanIT/StageFreight/src/sign"
)

// The public Sigstore services a keyless signature uses when no deployment is declared.
const (
	publicFulcio = "https://fulcio.sigstore.dev"
	publicOIDC   = "https://oauth2.sigstore.dev/auth"
)

// SigningConfigJSON is the Sigstore signing config (v0.2) naming a signature's services. A
// signature logged in public Sigstore uses the config Sigstore publishes; every other one
// names the declared deployment's services: a keyless signature its Fulcio and OIDC issuer
// (public Sigstore's when undeclared), and a logged one its Rekor.
func SigningConfigJSON(ctx context.Context, p sign.SignPlan, d SigstoreDeployment) ([]byte, error) {
	if p.TransparencyRequired && !d.Declared() {
		return publicSigningConfig(ctx)
	}
	type service struct {
		URL             string `json:"url"`
		MajorAPIVersion int    `json:"majorApiVersion"`
		ValidFor        struct {
			Start string `json:"start"`
		} `json:"validFor"`
		Operator string `json:"operator"`
	}
	operator := d.Domain
	if operator == "" {
		operator = "sigstore.dev"
	}
	svc := func(url string) []service {
		s := service{URL: url, MajorAPIVersion: 1, Operator: operator}
		s.ValidFor.Start = "1970-01-01T00:00:00Z"
		return []service{s}
	}
	cfg := struct {
		MediaType       string    `json:"mediaType"`
		CAURLs          []service `json:"caUrls"`
		OIDCURLs        []service `json:"oidcUrls"`
		RekorTlogURLs   []service `json:"rekorTlogUrls"`
		TSAURLs         []service `json:"tsaUrls"`
		RekorTlogConfig struct {
			Selector string `json:"selector"`
		} `json:"rekorTlogConfig"`
		TSAConfig struct {
			Selector string `json:"selector"`
		} `json:"tsaConfig"`
	}{
		MediaType: "application/vnd.dev.sigstore.signingconfig.v0.2+json",
		CAURLs:    []service{}, OIDCURLs: []service{}, RekorTlogURLs: []service{}, TSAURLs: []service{},
	}
	cfg.RekorTlogConfig.Selector, cfg.TSAConfig.Selector = "ANY", "ANY"

	if p.TrustClass == sign.ClassOIDC {
		fulcio, issuer := d.FulcioURL, d.OIDCIssuer
		if !d.Declared() {
			fulcio = publicFulcio
			if issuer == "" {
				issuer = publicOIDC
			}
		}
		if fulcio == "" {
			return nil, fmt.Errorf("keyless signing in the declared Sigstore deployment needs its Fulcio (SF_SIGSTORE_FULCIO)")
		}
		cfg.CAURLs = svc(fulcio)
		if issuer != "" {
			cfg.OIDCURLs = svc(issuer)
		}
	}
	if p.TransparencyRequired {
		if d.RekorURL == "" {
			return nil, fmt.Errorf("the plan requires a transparency log, and the declared Sigstore deployment names no Rekor (SF_SIGSTORE_REKOR)")
		}
		cfg.RekorTlogURLs = svc(d.RekorURL)
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// Signing configs public Sigstore publishes in its TUF repository: the default, and the one
// that writes to its Rekor v2 log while the default still names only v1.
const (
	publicConfigTarget     = "signing_config.v0.2.json"
	publicRekorV2Target    = "signing_config_rekor_v2.v0.2.json"
	rekorV2MajorAPIVersion = 2
)

// publicSigningConfig returns the signing config public Sigstore publishes, verified through
// its TUF repository, that writes to its Rekor v2 log: the default once it names one, else the
// Rekor v2 config. Fails closed: without a verified config the signature is not made.
var publicSigningConfig = func(ctx context.Context) ([]byte, error) {
	client, err := tuf.New(tuf.DefaultOptions().WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("public Sigstore's TUF repository: %w", err)
	}
	return selectPublicSigningConfig(client.GetTarget)
}

// selectPublicSigningConfig picks the published config that writes to Rekor v2.
func selectPublicSigningConfig(get func(target string) ([]byte, error)) ([]byte, error) {
	data, err := get(publicConfigTarget)
	if err != nil {
		return nil, fmt.Errorf("public Sigstore's signing config: %w", err)
	}
	if v2, err := namesRekorV2(data); err != nil {
		return nil, fmt.Errorf("public Sigstore's signing config: %w", err)
	} else if v2 {
		return data, nil
	}
	data, err = get(publicRekorV2Target)
	if err != nil {
		return nil, fmt.Errorf("public Sigstore's Rekor v2 signing config: %w", err)
	}
	if v2, err := namesRekorV2(data); err != nil {
		return nil, fmt.Errorf("public Sigstore's Rekor v2 signing config: %w", err)
	} else if !v2 {
		return nil, fmt.Errorf("public Sigstore's Rekor v2 signing config names no Rekor v2 log")
	}
	return data, nil
}

// namesRekorV2 reports whether a signing config names a Rekor v2 log.
func namesRekorV2(data []byte) (bool, error) {
	if _, err := root.NewSigningConfigFromJSON(data); err != nil {
		return false, err
	}
	var cfg struct {
		Rekor []struct {
			MajorAPIVersion int `json:"majorApiVersion"`
		} `json:"rekorTlogUrls"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return false, err
	}
	for _, r := range cfg.Rekor {
		if r.MajorAPIVersion == rekorV2MajorAPIVersion {
			return true, nil
		}
	}
	return false, nil
}
