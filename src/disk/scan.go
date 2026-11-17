package disk

import (
	"context"
	"path/filepath"
	"sync"
)

// Scan assembles the full report: the cache mount, the native user cache(s), the Docker
// daemon(s), and discovered repositories. The domains are independent, so they run
// concurrently — wall-clock is the slowest single domain, not their sum. Any
// domain that finds nothing is omitted. (Docker carries ctx's deadline and starts
// at t=0 alongside the walks, so the slow cache walk can't starve it.)
func Scan(ctx context.Context, cacheRoot string, repoRoots []string, maxDepth int) *Report {
	if cacheRoot == "" {
		cacheRoot = DiscoverCacheRoot()
	}

	// The native user cache fills whenever StageFreight runs outside CI, with or without
	// a mount. When the resolved cache root IS a user cache (native run, no mount), it is
	// scanned once, as the user cache.
	var userRoots []string
	for _, u := range UserCacheRoots() {
		if samePath(u, cacheRoot) {
			cacheRoot = ""
		}
		userRoots = append(userRoots, u)
	}
	userDoms := make([]*Node, len(userRoots))

	var dockerDom, cacheDom, repoDom *Node
	var wg sync.WaitGroup
	for i, u := range userRoots {
		wg.Add(1)
		go func() { defer wg.Done(); userDoms[i] = ScanUserCache(u) }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); dockerDom = ScanDocker(ctx) }()
	if cacheRoot != "" {
		wg.Add(1)
		go func() { defer wg.Done(); cacheDom = ScanCacheMount(cacheRoot) }()
	}
	if len(repoRoots) > 0 {
		wg.Add(1)
		go func() { defer wg.Done(); repoDom = ScanRepos(repoRoots, maxDepth) }()
	}
	wg.Wait()

	r := &Report{}
	if cacheDom != nil {
		r.Domains = append(r.Domains, cacheDom)
	}
	for _, d := range userDoms {
		if d != nil {
			r.Domains = append(r.Domains, d)
		}
	}
	if dockerDom != nil {
		r.Domains = append(r.Domains, dockerDom)
	}
	if repoDom != nil {
		r.Domains = append(r.Domains, repoDom)
	}
	// Scale bars against the filesystem holding the first real scan target.
	target := "/"
	for _, p := range append(append([]string{cacheRoot}, userRoots...), repoRoots...) {
		if p != "" && exists(p) {
			target = p
			break
		}
	}
	r.FS = statFS(target)
	return r
}

// samePath reports whether a and b name the same directory once symlinks resolve.
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}
