package forge

// ForgejoForge is Forgejo's first-class forge client.
//
// Forgejo is a Gitea fork and its REST API (/api/v1) is Gitea-compatible, so this
// client recycles GiteaForge as its backend and overrides only the identity it
// reports. The embedded *GiteaForge promotes the entire Forge interface
// implementation (releases, commits, MRs, tags, artifacts); ForgejoForge is the
// seam where Forgejo-specific behavior (federation, package registry, its own
// OIDC) will land when it diverges — without touching the Gitea client.
//
// This mirrors the render layer: a first-class identity package over a shared,
// internal backend. The reuse is an implementation detail; "forgejo" is a real
// provider with its own detection, config, and output everywhere a user looks.
type ForgejoForge struct {
	*GiteaForge
}

// NewForgejo creates a Forgejo client. It resolves config exactly like Gitea
// (the constructor already reads FORGEJO_TOKEN) and reports the Forgejo identity.
func NewForgejo(baseURL string) *ForgejoForge {
	return &ForgejoForge{GiteaForge: NewGitea(baseURL)}
}

// Provider reports Forgejo, overriding the embedded Gitea identity.
func (f *ForgejoForge) Provider() Provider { return Forgejo }
