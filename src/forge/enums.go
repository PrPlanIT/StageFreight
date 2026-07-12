package forge

// KnownProviders returns the recognized forge provider names — for docs and tooling that
// list allowed values from the authoritative source. Excludes the internal Unknown.
func KnownProviders() []string {
	return []string{
		string(GitLab), string(GitHub), string(Gitea), string(Forgejo), string(AzureDevOps),
	}
}
