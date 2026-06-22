package disk

import (
	"context"
	"sync"
)

// Scan assembles the full report: the cache mount, the Docker daemon(s), and
// discovered repositories. The three domains are independent, so they run
// concurrently — wall-clock is the slowest single domain, not their sum. Any
// domain that finds nothing is omitted. (Docker carries ctx's deadline and starts
// at t=0 alongside the walks, so the slow cache walk can't starve it.)
func Scan(ctx context.Context, cacheRoot string, repoRoots []string, maxDepth int) *Report {
	if cacheRoot == "" {
		cacheRoot = DiscoverCacheRoot()
	}

	var dockerDom, cacheDom, repoDom *Node
	var wg sync.WaitGroup
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
	if dockerDom != nil {
		r.Domains = append(r.Domains, dockerDom)
	}
	if repoDom != nil {
		r.Domains = append(r.Domains, repoDom)
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
