package forge

import "context"

// AccessLevel is what the authenticated identity may do on a repo, in forge-neutral steps.
type AccessLevel int

const (
	AccessNone     AccessLevel = iota
	AccessRead                 // read, triage, report
	AccessWrite                // push branches, open merge requests
	AccessMaintain             // merge, manage branches and releases
	AccessAdmin                // owner: settings and policy
)

// AccessReader is the OPTIONAL capability a forge implements when it can report the
// authenticated identity's access level on the repo. Callers type-assert it and treat a
// forge without it as unknown.
type AccessReader interface {
	Access(ctx context.Context) (AccessLevel, error)
}

// Access reads the token's permissions on the repo (GitHub: GET /repos/{o}/{r}).
func (g *GitHubForge) Access(ctx context.Context) (AccessLevel, error) {
	var repo struct {
		Permissions struct {
			Admin, Maintain, Push, Triage, Pull bool
		} `json:"permissions"`
	}
	if err := g.doJSON(ctx, "GET", g.apiURL(""), nil, &repo); err != nil {
		return AccessNone, err
	}
	p := repo.Permissions
	switch {
	case p.Admin:
		return AccessAdmin, nil
	case p.Maintain:
		return AccessMaintain, nil
	case p.Push:
		return AccessWrite, nil
	case p.Triage || p.Pull:
		return AccessRead, nil
	}
	return AccessNone, nil
}

// Access reads the token's access level on the project, direct or through its group,
// whichever is higher (GitLab: GET /projects/:id).
func (g *GitLabForge) Access(ctx context.Context) (AccessLevel, error) {
	type level struct {
		AccessLevel int `json:"access_level"`
	}
	var project struct {
		Permissions struct {
			Project *level `json:"project_access"`
			Group   *level `json:"group_access"`
		} `json:"permissions"`
	}
	if err := g.doJSON(ctx, "GET", g.apiURL(""), nil, &project); err != nil {
		return AccessNone, err
	}
	n := 0
	for _, l := range []*level{project.Permissions.Project, project.Permissions.Group} {
		if l != nil && l.AccessLevel > n {
			n = l.AccessLevel
		}
	}
	// GitLab levels: 10 guest, 15 planner, 20 reporter, 30 developer, 40 maintainer, 50 owner.
	switch {
	case n >= 50:
		return AccessAdmin, nil
	case n >= 40:
		return AccessMaintain, nil
	case n >= 30:
		return AccessWrite, nil
	case n > 0:
		return AccessRead, nil
	}
	return AccessNone, nil
}

// Access reads the token's permissions on the repo (Gitea/Forgejo: GET /repos/{o}/{r}).
// Gitea has no maintainer tier: admin is the repo's owner or an admin collaborator.
func (g *GiteaForge) Access(ctx context.Context) (AccessLevel, error) {
	var repo struct {
		Permissions struct {
			Admin, Push, Pull bool
		} `json:"permissions"`
	}
	if err := g.doJSON(ctx, "GET", g.apiURL(""), nil, &repo); err != nil {
		return AccessNone, err
	}
	p := repo.Permissions
	switch {
	case p.Admin:
		return AccessAdmin, nil
	case p.Push:
		return AccessWrite, nil
	case p.Pull:
		return AccessRead, nil
	}
	return AccessNone, nil
}
