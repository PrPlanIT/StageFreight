package freshness

import (
	"context"
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

// ResolveDeps runs the full dependency resolution pipeline across all files
// and returns raw Dependency structs with vulnerabilities correlated.
// opts is passed to Configure; nil uses FreshnessConfig defaults.
func ResolveDeps(ctx context.Context, opts map[string]any, files []lint.FileInfo) ([]Dependency, error) {
	m := newModule()
	if opts != nil {
		if err := m.Configure(opts); err != nil {
			return nil, err
		}
	}
	if m.http == nil {
		m.http = newHTTPClient(m.cfg.Timeout)
	}

	var all []Dependency
	for _, f := range files {
		deps, err := m.resolveFile(ctx, f)
		if err != nil {
			return nil, fmt.Errorf("resolving %s: %w", f.Path, err)
		}
		all = append(all, deps...)
	}

	m.correlateVulns(ctx, all)
	return all, nil
}
