package facts

import (
	"context"
	"strconv"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/k8s"
)

// Inventory metadata for badges is addressed by gitops cluster name:
// {inventory.<cluster>.count} → the number of applications the k8s inventory
// discovers on that cluster. <cluster> is a gitops: map key (the same name the
// k8s-inventory region reports as "Apps & Services (N)").
const invPrefix = "{inventory."

// invFields are the recognized {inventory.<cluster>.<field>} suffixes.
var invFields = []string{"count"}

// ResolveInventoryTemplates resolves {inventory.<cluster>.count} tokens across the
// given badge values, discovering each referenced gitops cluster's live inventory
// exactly once. An unknown cluster or a discovery failure leaves the token
// unresolved, which the badge layer renders as "n/a".
func ResolveInventoryTemplates(ctx context.Context, values []string, appCfg *config.Config, rootDir string) []string {
	names := extractInventoryClusters(values)
	if len(names) == 0 {
		return values
	}
	catalog := firstK8sInventoryCatalog(appCfg)
	counts := make(map[string]int, len(names))
	for name := range names {
		cluster, ok := appCfg.GitOps.ByName(name)
		if !ok {
			continue
		}
		client, err := k8s.NewClient(cluster.Connection())
		if err != nil {
			continue
		}
		result, err := k8s.Discover(ctx, client, catalog, rootDir, cluster.Exposure)
		if err != nil || result == nil {
			continue
		}
		counts[name] = len(result.Apps)
	}
	if len(counts) == 0 {
		return values
	}
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = resolveInventoryTokens(v, counts)
	}
	return out
}

// parseInventoryToken splits the inside of an {inventory.…} token (without braces)
// into its cluster name and field. The field is suffix-anchored (a cluster name may
// itself contain dots), so "dungeon.count" → ("dungeon", "count").
func parseInventoryToken(inner string) (cluster, field string) {
	for _, f := range invFields {
		if strings.HasSuffix(inner, "."+f) {
			return inner[:len(inner)-len(f)-1], f
		}
	}
	return "", ""
}

// extractInventoryClusters scans all values for {inventory.<cluster>.<field>} tokens,
// returning the set of referenced cluster names.
func extractInventoryClusters(values []string) map[string]bool {
	names := map[string]bool{}
	for _, v := range values {
		s := v
		for {
			i := strings.Index(s, invPrefix)
			if i == -1 {
				break
			}
			rest := s[i+len(invPrefix):]
			end := strings.IndexByte(rest, '}')
			if end == -1 {
				break
			}
			if cluster, field := parseInventoryToken(rest[:end]); cluster != "" && field != "" {
				names[cluster] = true
			}
			s = rest[end+1:]
		}
	}
	return names
}

// resolveInventoryTokens substitutes every resolvable {inventory.<cluster>.count}
// token in v; unresolved clusters keep the literal token (→ "n/a" at the badge layer).
func resolveInventoryTokens(v string, counts map[string]int) string {
	var b strings.Builder
	s := v
	for {
		i := strings.Index(s, invPrefix)
		if i == -1 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		rest := s[i+len(invPrefix):]
		end := strings.IndexByte(rest, '}')
		if end == -1 {
			b.WriteString(s[i:])
			break
		}
		cluster, field := parseInventoryToken(rest[:end])
		if field == "count" {
			if n, ok := counts[cluster]; ok {
				b.WriteString(strconv.Itoa(n))
				s = rest[end+1:]
				continue
			}
		}
		// Unresolved — keep the literal token so the badge layer renders "n/a".
		b.WriteString(s[i : i+len(invPrefix)+end+1])
		s = rest[end+1:]
	}
	return b.String()
}

// firstK8sInventoryCatalog returns the catalog path of the first k8s-inventory
// stencil (catalog is override-only naming, so any is fine for a count).
func firstK8sInventoryCatalog(appCfg *config.Config) string {
	for _, st := range appCfg.Stencils {
		if st.EffectiveKind() == "k8s-inventory" {
			return st.CatalogPath
		}
	}
	return ""
}
