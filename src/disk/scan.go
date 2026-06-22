package disk

import "context"

// Scan assembles the full report: the cache mount, the Docker daemon(s), and
// discovered repositories. Any domain that finds nothing is omitted.
func Scan(ctx context.Context, cacheRoot string, repoRoots []string, maxDepth int) *Report {
	// Scan Docker FIRST, while ctx still has its full timeout budget. The cache walk
	// below can run for many seconds on a millions-of-files cache (it stats every
	// entry for du-accurate block sizing) and would otherwise consume the docker
	// commands' deadline, making the whole DOCKER domain silently disappear.
	dockerDom := ScanDocker(ctx)

	if cacheRoot == "" {
		cacheRoot = DiscoverCacheRoot()
	}
	r := &Report{}
	if cacheRoot != "" {
		if c := ScanCacheMount(cacheRoot); c != nil {
			r.Domains = append(r.Domains, c)
		}
	}
	if dockerDom != nil {
		r.Domains = append(r.Domains, dockerDom)
	}
	if len(repoRoots) > 0 {
		if rp := ScanRepos(repoRoots, maxDepth); rp != nil {
			r.Domains = append(r.Domains, rp)
		}
	}
	// Scale bars against the filesystem holding the first real scan target.
	target := "/"
	for _, p := range append([]string{cacheRoot}, repoRoots...) {
		if p != "" && exists(p) {
			target = p
			break
		}
	}
	r.FS = statFS(target)
	return r
}
