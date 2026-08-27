package governance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/ci/render"
	"github.com/PrPlanIT/StageFreight/src/config"
)

// Governance distributes to many repos at once, which makes an invalid render a FLEET
// outage rather than one repo's problem: every satellite receives the same broken config
// in the same reconcile and every pipeline fails at load, before anything runs. A single
// wrong reference in the control repo's catalog — a contents stencil naming a build the
// shared preset does not declare — is enough to do it.
//
// The reconcile plan alone cannot catch that. It reports which FILES change, not whether
// what they contain will load, so a config that cannot parse looks identical in the plan
// to one that can. The only honest check is to load the rendered bytes exactly as the
// satellite will, which means materializing them next to the preset cache that ships with
// them — a governed config is meaningless without those presets, since it references them
// by path.
//
// Failing here costs one reconcile. Not failing here costs every governed repo.

// loadSatelliteConfig loads a rendered satellite config the way the satellite will, with
// its preset cache in place, and returns an error naming the repo when it fails. The
// loaded config is returned so the caller can derive from the SAME config the satellite
// will see — notably the CI skeleton — instead of materializing it a second time.
func loadSatelliteConfig(repo string, sealed []byte, presetFiles map[string][]byte) (*config.Config, error) {
	dir, err := os.MkdirTemp("", "sf-govval-")
	if err != nil {
		// Cannot verify — say so rather than reporting a pass we did not perform.
		return nil, fmt.Errorf("cannot verify rendered config for %s: %w", repo, err)
	}
	defer os.RemoveAll(dir)

	// The preset cache must land first: the config references these by path, so loading
	// without them fails for a reason that has nothing to do with the config's validity.
	for rel, content := range presetFiles {
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, fmt.Errorf("cannot verify rendered config for %s: %w", repo, err)
		}
		if err := os.WriteFile(dst, content, 0o644); err != nil {
			return nil, fmt.Errorf("cannot verify rendered config for %s: %w", repo, err)
		}
	}

	cfgPath := filepath.Join(dir, ".stagefreight.yml")
	if err := os.WriteFile(cfgPath, sealed, 0o644); err != nil {
		return nil, fmt.Errorf("cannot verify rendered config for %s: %w", repo, err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("rendered config for %s would not load: %w\n"+
			"  the satellite would fail at audition before running anything — fix the profile or the catalog entry and reconcile again", repo, err)
	}
	return cfg, nil
}

// renderSatelliteCI renders the forge-native pipeline the satellite's config declares.
//
// Governance distributes .stagefreight.yml, and the CI file is DERIVED from it — so
// distributing one without the other leaves the satellite self-inconsistent and audition
// rejects it: "CI is stale: .gitlab-ci.yml does not match render output". Rendering it
// here keeps the two in lockstep by construction, which is the only way they stay in step
// across a fleet nobody re-renders by hand.
//
// Returns nothing when the config declares no ci.forges: without that declaration there
// is nothing to render, and inferring one would write a skeleton for a forge that may
// never run it.
func renderSatelliteCI(repo string, cfg *config.Config) ([]satelliteCIFile, error) {
	if len(cfg.CI.Forges) == 0 {
		return nil, nil
	}
	plan, err := render.Plan(cfg)
	if err != nil {
		return nil, fmt.Errorf("%s: planning CI pipeline: %w", repo, err)
	}

	var out []satelliteCIFile
	seen := map[string]bool{}
	for _, raw := range cfg.CI.Forges {
		forge := strings.TrimSpace(raw)
		if forge == "" {
			return nil, fmt.Errorf("%s: ci.forges: empty entry (supported: %s)", repo, strings.Join(render.SupportedForges, ", "))
		}
		// A repeated forge would render the same path twice and the second write would
		// silently win. It is a typo, not an intent — name it.
		if seen[forge] {
			return nil, fmt.Errorf("%s: ci.forges: %q listed more than once", repo, forge)
		}
		seen[forge] = true

		target, err := render.ForgeTarget(forge)
		if err != nil {
			return nil, fmt.Errorf("%s: ci.forges: %w (supported: %s)", repo, err, strings.Join(render.SupportedForges, ", "))
		}
		content, err := render.Emit(forge, plan)
		if err != nil {
			return nil, fmt.Errorf("%s: rendering %s pipeline: %w", repo, forge, err)
		}
		out = append(out, satelliteCIFile{Path: target, Content: content})
	}
	return out, nil
}

// satelliteCIFile is one rendered pipeline destined for a satellite.
type satelliteCIFile struct {
	Path    string
	Content []byte
}
