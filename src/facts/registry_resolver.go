package facts

import "context"

// registryResolver resolves {registry.<id>.*} across a value set, fetching each
// referenced registry's metadata exactly once. It wraps ResolveRegistryTemplates
// (which is inherently batch — the fetch-once contract is why the registry operates
// on []string rather than one value at a time).
type registryResolver struct{}

// RegistryResolver returns the resolver for the {registry.*} family.
func RegistryResolver() Resolver { return registryResolver{} }

func (registryResolver) Name() string         { return "registry" }
func (registryResolver) Provides() []string   { return []string{"registry"} }
func (registryResolver) DependsOn() []string  { return nil }
func (registryResolver) Resolve(values []string, c *Context) []string {
	if c == nil || c.Config == nil {
		return values
	}
	return ResolveRegistryTemplates(ctxOf(c), values, c.Config)
}

// inventoryResolver resolves {inventory.<cluster>.count} across a value set,
// discovering each referenced gitops cluster's live inventory once. Also inherently
// batch (discover-once), hence the []string resolver shape.
type inventoryResolver struct{}

// InventoryResolver returns the resolver for the {inventory.*} family.
func InventoryResolver() Resolver { return inventoryResolver{} }

func (inventoryResolver) Name() string         { return "inventory" }
func (inventoryResolver) Provides() []string   { return []string{"inventory"} }
func (inventoryResolver) DependsOn() []string  { return nil }
func (inventoryResolver) Resolve(values []string, c *Context) []string {
	if c == nil || c.Config == nil {
		return values
	}
	return ResolveInventoryTemplates(ctxOf(c), values, c.Config, c.RootDir)
}

// ctxOf returns the Context's carried context.Context, or Background when unset, so
// live-fetch resolvers always have a non-nil context to scope their calls.
func ctxOf(c *Context) context.Context {
	if c != nil && c.Ctx != nil {
		return c.Ctx
	}
	return context.Background()
}
