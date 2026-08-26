package facts

// BadgeRegistry returns the standard registry for resolving badge values: the gitver
// leaf pass (version/vars/commit/project/…), then the {registry.*} and {inventory.*}
// batch resolvers. This is the single assembly point for the badge fact set — the
// order (leaf → registry → inventory) matches the pre-registry pipeline and, since
// these families are independent, is fixed by registration order.
//
// Identity families ({org.*} → {path.*} → {metadata.*}) register here once they land,
// with declared dependencies so the topological order places them correctly.
func BadgeRegistry() *Registry {
	return New().
		Add(GitverLeaf()).
		Add(RegistryResolver()).
		Add(InventoryResolver())
}
