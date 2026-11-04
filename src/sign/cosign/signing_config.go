package cosign

import (
	"encoding/json"
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/sign"
)

// The public Sigstore services a keyless signature uses when no deployment is declared.
const (
	publicFulcio = "https://fulcio.sigstore.dev"
	publicOIDC   = "https://oauth2.sigstore.dev/auth"
)

// NeedsSigningConfig reports whether cosign must be handed a signing config naming its
// services, in place of the TUF-provided one: a key, KMS or hardware signature without a
// transparency log, and a keyless signature in a declared (self-hosted) deployment or
// without a transparency log.
func NeedsSigningConfig(p sign.SignPlan, d SigstoreDeployment) bool {
	if p.TrustClass == sign.ClassOIDC {
		return d.Declared() || !p.TransparencyRequired
	}
	return !p.TransparencyRequired
}

// SigningConfigJSON is the Sigstore signing config (v0.2) for a plan: no services for a
// key, KMS or hardware signature; for a keyless one, the deployment's Fulcio and OIDC
// issuer (public Sigstore's when undeclared), and its Rekor when transparency is required.
func SigningConfigJSON(p sign.SignPlan, d SigstoreDeployment) ([]byte, error) {
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
		if p.TransparencyRequired {
			if d.RekorURL == "" {
				return nil, fmt.Errorf("the plan requires a transparency log, and the declared Sigstore deployment names no Rekor (SF_SIGSTORE_REKOR)")
			}
			cfg.RekorTlogURLs = svc(d.RekorURL)
		}
	}
	return json.MarshalIndent(cfg, "", "  ")
}
