package discovery

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/lint"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

// Resolver runs the dependency-resolution engine: per-ecosystem checkers,
// registry lookups, and vulnerability correlation. It holds no lint
// dependency beyond lint.FileInfo, the resolver input type.
type Resolver struct {
	cfg     FreshnessConfig
	http    *httpClient
	desired map[string]config.ToolPinConfig
	now     func() time.Time // injectable clock for cooldown evaluation; nil → time.Now
}

// NewResolver constructs a Resolver with default configuration.
func NewResolver() *Resolver {
	return &Resolver{cfg: DefaultConfig()}
}

// clock returns the current time, honoring an injected test clock.
func (m *Resolver) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

// SetToolchainDesired records the toolchains.desired config used by
// checkToolchainDesired.
func (m *Resolver) SetToolchainDesired(desired map[string]config.ToolPinConfig) {
	m.desired = desired
}

// Configure applies the parsed FreshnessConfig options.
func (m *Resolver) Configure(opts map[string]any) error {
	cfg, err := parseConfig(opts)
	if err != nil {
		return err
	}
	m.cfg = cfg
	m.http = newHTTPClient(cfg.Timeout)
	return nil
}

// Config returns the resolver's active configuration.
func (m *Resolver) Config() *FreshnessConfig {
	return &m.cfg
}

// ResolveFile resolves dependencies for a single file using the Resolver's
// current configuration, correlating vulnerabilities. Lazily initializes the
// HTTP client if Configure was not called (defaults). This is the entry point
// used by the freshness lint renderer's per-file Check.
func (m *Resolver) ResolveFile(ctx context.Context, file lint.FileInfo) ([]supplychain.Dependency, error) {
	if m.http == nil {
		m.http = newHTTPClient(m.cfg.Timeout)
	}
	deps, err := m.resolveFile(ctx, file)
	if err != nil {
		return nil, err
	}
	m.correlateVulns(ctx, deps)
	return deps, nil
}

// resolveFile dispatches to the appropriate checker based on filename
// and returns raw Dependency structs (no lint-finding conversion).
func (m *Resolver) resolveFile(ctx context.Context, file lint.FileInfo) ([]supplychain.Dependency, error) {
	base := filepath.Base(file.Path)
	switch {
	case isDockerfile(base):
		return m.checkDockerfile(ctx, file)
	case base == "go.mod":
		return m.checkGoMod(ctx, file)
	case base == ".stagefreight.yml":
		// Toolchain desired versions — config-driven discovery
		return m.checkToolchainDesired(ctx, m.desired), nil
	case base == "Cargo.toml":
		return m.checkCargo(ctx, file)
	case base == "package.json":
		return m.checkNpm(ctx, file)
	case base == "requirements.txt" || strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt"):
		return m.checkPip(ctx, file)
	case base == "Pipfile":
		return m.checkPip(ctx, file)
	default:
		return nil, nil
	}
}

// isDockerfile returns true for Dockerfile, Dockerfile.*, and *.dockerfile.
func isDockerfile(base string) bool {
	if base == "Dockerfile" || strings.HasPrefix(base, "Dockerfile.") {
		return true
	}
	return strings.HasSuffix(base, ".dockerfile")
}

// Resolve runs the full dependency resolution pipeline across all files
// and returns raw Dependency structs with vulnerabilities correlated.
// opts is passed to Configure; nil uses FreshnessConfig defaults.
func Resolve(ctx context.Context, opts map[string]any, files []lint.FileInfo) ([]supplychain.Dependency, error) {
	m := NewResolver()
	if opts != nil {
		if err := m.Configure(opts); err != nil {
			return nil, err
		}
	}
	if m.http == nil {
		m.http = newHTTPClient(m.cfg.Timeout)
	}

	var all []supplychain.Dependency
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
