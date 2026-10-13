package k8s

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitops"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayclient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"
)

const (
	maxOwnerRefDepth  = 10
	labelInstance     = "app.kubernetes.io/instance"
	labelName         = "app.kubernetes.io/name"
	labelVersion      = "app.kubernetes.io/version"
	labelHelmChart    = "helm.sh/chart"
	labelTierOverride = "scribe.stagefreight.io/tier"
)

// Discover queries the live cluster and returns a complete DiscoveryResult.
// ObservedAt is captured once at start. Uses client-go directly.
func Discover(ctx context.Context, client *Client, catalogPath, repoRoot string, cluster config.ClusterConfig) (*DiscoveryResult, error) {
	observedAt := time.Now()

	// Load catalog
	catalog, err := LoadCatalog(catalogPath)
	if err != nil {
		return nil, fmt.Errorf("loading catalog: %w", err)
	}

	// Phase 1: Discover workloads (only seeds)
	groups, err := discoverWorkloads(ctx, client.Clientset)
	if err != nil {
		return nil, err
	}

	// Phase 1.5: Prefix-based family grouping for unlabeled multi-component apps.
	// Only touches name-fallback workloads. Never merges into labeled identities.
	groupByPrefix(groups)

	// Phase 2: Augment with services
	if err := augmentServices(ctx, client.Clientset, groups); err != nil {
		return nil, err
	}

	// Phase 3: Augment with HTTPRoutes and Ingresses. Either API may be absent or not
	// readable; routes only add hostnames and exposure, never whether an app exists.
	if err := augmentHTTPRoutes(ctx, client.GatewayClient, client.Clientset, groups); err != nil {
		_ = err
	}
	if err := augmentIngresses(ctx, client.Clientset, groups); err != nil {
		_ = err
	}

	// Phase 4: Resolve declared sources from Flux graph (local repo walk)
	fluxSources := resolveFluxSources(repoRoot, groups)

	// Phase 5: Build AppRecords, classify, apply catalog
	resolver := NewCategoryResolver(cluster.Categories)
	records := buildRecords(groups, catalog, resolver, fluxSources, cluster.Exposure)

	// Phase 5.5: Fold subordinate-only apps into their parents.
	// Must happen after buildRecords (need roles) and before tier separation.
	records = FoldSubordinates(records)

	// Phase 6: Separate into tiers
	var apps, platform, unresolved []AppRecord
	for _, r := range records {
		switch r.Tier {
		case TierApp:
			apps = append(apps, r)
		case TierPlatform:
			platform = append(platform, r)
		case TierUnresolved:
			unresolved = append(unresolved, r)
		case TierHidden:
			// excluded
		}
	}

	sortRecords(apps, resolver)
	sortRecords(platform, resolver)
	sortRecords(unresolved, resolver)

	// Phase 7: Reconcile lifecycle state + derive graveyard.
	// Scoped to cluster: .stagefreight/inventory/k8s-inventory-<cluster>.json
	allActive := append(append(append([]AppRecord{}, apps...), platform...), unresolved...)
	var graveyard []GraveyardEntry

	if repoRoot != "" && client.ClusterName != "" {
		manifest, err := LoadManifest(repoRoot, client.ClusterName)
		if err == nil {
			// true is safe here, not merely convenient: every source of app EXISTENCE
			// above is fatal. Deployments, StatefulSets and DaemonSets each return
			// nil, err on a list failure, as does augmentServices, so this line is
			// unreachable unless the sweep saw the whole cluster. A degraded sweep
			// aborts before Phase 7 rather than arriving here with a short list and
			// graveyarding what it failed to see.
			//
			// The one tolerated failure, augmentHTTPRoutes, affects exposure metadata
			// and never whether an app appears in allActive.
			//
			// So the parameter is not inert — it guards a caller that does not exist
			// yet. If a workload phase is ever made non-fatal the way augmentHTTPRoutes
			// is, this literal becomes wrong silently, and completeness must then be
			// derived from the sweep instead of asserted here.
			changed := ReconcileLifecycle(manifest, allActive, true, observedAt)
			if changed {
				_ = SaveManifest(repoRoot, client.ClusterName, manifest)
			}
			graveyard = GraveyardFromManifest(manifest)
		}
	}

	return &DiscoveryResult{
		Apps:       apps,
		Platform:   platform,
		Unresolved: unresolved,
		Graveyard:  graveyard,
		ObservedAt: observedAt,
		Cluster:    client.ClusterName,
	}, nil
}

// appGroup accumulates resources for a single app identity during discovery.
type appGroup struct {
	identity  WorkloadIdentity
	workloads []workloadInfo
	services  []corev1.Service
	routes    []ExposureRef
	podLabels map[string]string // merged pod template labels from first workload
}

type workloadInfo struct {
	name           string
	kind           string
	replicas       int32
	ready          int32
	containers     []corev1.Container
	initContainers []corev1.Container
	labels         map[string]string
}

// discoverWorkloads lists all Deployments, StatefulSets, DaemonSets and
// groups them by resolved app identity.
func discoverWorkloads(ctx context.Context, cs kubernetes.Interface) (map[AppKey]*appGroup, error) {
	groups := map[AppKey]*appGroup{}

	// Deployments
	deploys, err := cs.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing deployments: %w", err)
	}
	for i := range deploys.Items {
		d := &deploys.Items[i]
		id := resolveIdentity(d.Namespace, d.Name, d.Labels, d.OwnerReferences, "Deployment", ctx, cs)
		addWorkload(groups, id, workloadInfo{
			name:           d.Name,
			kind:           "Deployment",
			replicas:       ptr32(d.Spec.Replicas, 1),
			ready:          d.Status.ReadyReplicas,
			containers:     d.Spec.Template.Spec.Containers,
			initContainers: d.Spec.Template.Spec.InitContainers,
			labels:         d.Spec.Template.Labels,
		})
	}

	// StatefulSets
	stss, err := cs.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing statefulsets: %w", err)
	}
	for i := range stss.Items {
		s := &stss.Items[i]
		id := resolveIdentity(s.Namespace, s.Name, s.Labels, s.OwnerReferences, "StatefulSet", ctx, cs)
		addWorkload(groups, id, workloadInfo{
			name:           s.Name,
			kind:           "StatefulSet",
			replicas:       ptr32(s.Spec.Replicas, 1),
			ready:          s.Status.ReadyReplicas,
			containers:     s.Spec.Template.Spec.Containers,
			initContainers: s.Spec.Template.Spec.InitContainers,
			labels:         s.Spec.Template.Labels,
		})
	}

	// DaemonSets
	dss, err := cs.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing daemonsets: %w", err)
	}
	for i := range dss.Items {
		d := &dss.Items[i]
		id := resolveIdentity(d.Namespace, d.Name, d.Labels, d.OwnerReferences, "DaemonSet", ctx, cs)
		addWorkload(groups, id, workloadInfo{
			name:           d.Name,
			kind:           "DaemonSet",
			replicas:       d.Status.DesiredNumberScheduled,
			ready:          d.Status.NumberReady,
			containers:     d.Spec.Template.Spec.Containers,
			initContainers: d.Spec.Template.Spec.InitContainers,
			labels:         d.Spec.Template.Labels,
		})
	}

	return groups, nil
}

// resolveIdentity determines the app identity for a workload.
// Strict precedence, frozen once resolved.
func resolveIdentity(ns, name string, labels map[string]string, owners []metav1.OwnerReference, kind string, ctx context.Context, cs kubernetes.Interface) WorkloadIdentity {
	// 1. app.kubernetes.io/instance
	if v, ok := labels[labelInstance]; ok && v != "" {
		return WorkloadIdentity{
			Key:     AppKey{Namespace: ns, Identity: v},
			Source:  "label/instance",
			RootUID: resolveRootUID(ns, owners, ctx, cs),
		}
	}

	// 2. app.kubernetes.io/name
	if v, ok := labels[labelName]; ok && v != "" {
		return WorkloadIdentity{
			Key:     AppKey{Namespace: ns, Identity: v},
			Source:  "label/name",
			RootUID: resolveRootUID(ns, owners, ctx, cs),
		}
	}

	// 3. helm.sh/chart family
	if v, ok := labels[labelHelmChart]; ok && v != "" {
		// chart label is "name-version", extract name
		chartName := v
		if idx := strings.LastIndex(v, "-"); idx > 0 {
			chartName = v[:idx]
		}
		return WorkloadIdentity{
			Key:     AppKey{Namespace: ns, Identity: chartName},
			Source:  "helm",
			RootUID: resolveRootUID(ns, owners, ctx, cs),
		}
	}

	// 4. ownerRef root workload name
	if rootName, rootUID := walkOwnerRefs(ns, owners, ctx, cs); rootName != "" {
		return WorkloadIdentity{
			Key:     AppKey{Namespace: ns, Identity: rootName},
			Source:  "ownerRef",
			RootUID: rootUID,
		}
	}

	// 5. workload name fallback
	return WorkloadIdentity{
		Key:    AppKey{Namespace: ns, Identity: name},
		Source: "name",
	}
}

// resolveRootUID walks ownerRefs to find the root UID for collision detection.
func resolveRootUID(ns string, owners []metav1.OwnerReference, ctx context.Context, cs kubernetes.Interface) string {
	_, uid := walkOwnerRefs(ns, owners, ctx, cs)
	return uid
}

// walkOwnerRefs traverses ownerReferences to the root controller.
// Guards against cycles (visited set) and depth (maxOwnerRefDepth).
func walkOwnerRefs(ns string, owners []metav1.OwnerReference, ctx context.Context, cs kubernetes.Interface) (string, string) {
	visited := map[types.UID]bool{}
	currentOwners := owners
	currentNS := ns
	var lastName, lastUID string

	for depth := 0; depth < maxOwnerRefDepth && len(currentOwners) > 0; depth++ {
		// Find the controller owner
		var controller *metav1.OwnerReference
		for i := range currentOwners {
			if currentOwners[i].Controller != nil && *currentOwners[i].Controller {
				controller = &currentOwners[i]
				break
			}
		}
		if controller == nil {
			break
		}

		uid := controller.UID
		if visited[uid] {
			break // cycle detected
		}
		visited[uid] = true
		lastName = controller.Name
		lastUID = string(uid)

		// Walk up: try to get the owner's ownerReferences
		switch controller.Kind {
		case "ReplicaSet":
			rs, err := cs.AppsV1().ReplicaSets(currentNS).Get(ctx, controller.Name, metav1.GetOptions{})
			if err != nil {
				return lastName, lastUID
			}
			currentOwners = rs.OwnerReferences
		case "Deployment":
			return controller.Name, string(controller.UID)
		case "StatefulSet":
			return controller.Name, string(controller.UID)
		case "DaemonSet":
			return controller.Name, string(controller.UID)
		default:
			return lastName, lastUID
		}
	}

	return lastName, lastUID
}

// addWorkload adds a workload to the appropriate app group, handling collision detection.
func addWorkload(groups map[AppKey]*appGroup, id WorkloadIdentity, wl workloadInfo) {
	key := id.Key

	existing, ok := groups[key]
	if ok && id.RootUID != "" && existing.identity.RootUID != "" && id.RootUID != existing.identity.RootUID {
		// Collision: same identity but different root owner.
		// Disambiguate with short UID suffix.
		key.Identity = key.Identity + "#" + id.RootUID[:8]
		id.Key = key
	}

	g, ok := groups[key]
	if !ok {
		g = &appGroup{
			identity:  id,
			podLabels: wl.labels,
		}
		groups[key] = g
	}

	g.workloads = append(g.workloads, wl)
}

// genericPrefixDenylist contains tokens that are too generic to be family prefixes.
var genericPrefixDenylist = map[string]bool{
	"api": true, "web": true, "app": true, "service": true,
	"backend": true, "frontend": true, "worker": true, "server": true,
	"db": true, "cache": true, "proxy": true, "ingress": true,
}

// groupByPrefix merges unlabeled multi-component workloads into family groups.
// Only touches workloads with Source == "name" (label-based identities are frozen).
// Never merges into existing labeled identities.
// Uses longest common hyphen-boundary prefix (LCP) instead of first-segment extraction
// to preserve multi-segment identities like "open-webui" and "reactive-resume".
func groupByPrefix(groups map[AppKey]*appGroup) {
	// Collect name-fallback groups per namespace.
	type candidate struct {
		key   AppKey
		group *appGroup
	}
	byNS := map[string][]candidate{}

	for key, g := range groups {
		if g.identity.Source == "name" {
			byNS[key.Namespace] = append(byNS[key.Namespace], candidate{key: key, group: g})
		}
	}

	for _, candidates := range byNS {
		if len(candidates) < 2 {
			continue
		}
		// Fixed member order: the family inherits the first member's labels and lists
		// its workloads in merge order, so an order taken from map iteration would
		// leak into every downstream derivation.
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].key.less(candidates[j].key) })

		// Bucket candidates by first hyphen segment for initial grouping.
		firstSegBuckets := map[string][]candidate{}
		for _, c := range candidates {
			seg := firstSegment(c.key.Identity)
			if seg == "" || genericPrefixDenylist[seg] {
				continue
			}
			firstSegBuckets[seg] = append(firstSegBuckets[seg], c)
		}

		// Within each bucket, compute the longest common hyphen-boundary prefix.
		for _, members := range firstSegBuckets {
			if len(members) < 2 {
				continue
			}

			names := make([]string, len(members))
			for i, m := range members {
				names[i] = m.key.Identity
			}
			prefix := longestCommonHyphenPrefix(names)
			if prefix == "" || len(prefix) < 3 || genericPrefixDenylist[prefix] {
				continue
			}

			// Verify all members have a suffix beyond the prefix.
			allHaveSuffix := true
			for _, m := range members {
				if m.key.Identity == prefix {
					allHaveSuffix = false
					break
				}
			}
			if !allHaveSuffix {
				continue
			}

			// Ensure prefix doesn't collide with an existing labeled identity.
			familyKey := AppKey{Namespace: members[0].key.Namespace, Identity: prefix}
			if existing, ok := groups[familyKey]; ok && existing.identity.Source != "name" {
				continue // labeled identity exists — never merge into it
			}

			// Create or find the family group.
			family, exists := groups[familyKey]
			if !exists {
				family = &appGroup{
					identity: WorkloadIdentity{
						Key:    familyKey,
						Source: "prefix",
					},
				}
			}

			// Merge members into family.
			for _, m := range members {
				for _, wl := range m.group.workloads {
					family.workloads = append(family.workloads, wl)
				}
				family.routes = append(family.routes, m.group.routes...)
				family.services = append(family.services, m.group.services...)
				if family.podLabels == nil && m.group.podLabels != nil {
					family.podLabels = m.group.podLabels
				}

				if m.key != familyKey {
					delete(groups, m.key)
				}
			}

			groups[familyKey] = family
		}
	}
}

// firstSegment returns the first hyphen-delimited segment of a name.
// "tactical-backend" → "tactical", "standalone" → ""
func firstSegment(name string) string {
	idx := strings.Index(name, "-")
	if idx <= 0 || idx >= len(name)-1 {
		return ""
	}
	seg := name[:idx]
	if len(seg) < 3 {
		return ""
	}
	return seg
}

// augmentServices attaches Services to app groups via strict selector matching.
func augmentServices(ctx context.Context, cs kubernetes.Interface, groups map[AppKey]*appGroup) error {
	svcs, err := cs.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("listing services: %w", err)
	}

	for i := range svcs.Items {
		svc := &svcs.Items[i]
		if len(svc.Spec.Selector) == 0 {
			continue // no-selector services ignored
		}
		if key, ok := serviceOwner(groups, svc); ok {
			groups[key].services = append(groups[key].services, *svc)
		}
	}

	return nil
}

// serviceOwner picks the ONE group a Service belongs to: the selector must be a subset
// of the labels of some workload in the group. A merged family (indexer + manager +
// dashboard) carries one label set per member and a per-component Service selects
// exactly one of them, so every member is consulted, not just whichever one happened
// to be merged first. If distinct groups both qualify, the lowest key wins — a fixed
// rule, so the same cluster state yields the same owner on every run. Ownership decides
// which app the HTTPRoutes (hostnames, exposure) attach to, and through rec.Hosts even
// the app/platform tier; an owner that varied with map order made all of those flip
// between consecutive renders of an unchanged cluster.
func serviceOwner(groups map[AppKey]*appGroup, svc *corev1.Service) (AppKey, bool) {
	var best AppKey
	found := false
	for key, g := range groups {
		if key.Namespace != svc.Namespace || !g.matchesSelector(svc.Spec.Selector) {
			continue
		}
		if !found || key.less(best) {
			best, found = key, true
		}
	}
	return best, found
}

// matchesSelector reports whether the selector selects any workload in the group.
func (g *appGroup) matchesSelector(selector map[string]string) bool {
	for _, wl := range g.workloads {
		if selectorMatches(selector, wl.labels) {
			return true
		}
	}
	return len(g.workloads) == 0 && selectorMatches(selector, g.podLabels)
}

// less orders keys by namespace, then identity — the one tiebreak every sort here uses.
func (k AppKey) less(o AppKey) bool {
	if k.Namespace != o.Namespace {
		return k.Namespace < o.Namespace
	}
	return k.Identity < o.Identity
}

// augmentHTTPRoutes attaches HTTPRoute hostnames to app groups.
// Validates: route backendRefs → Service → selector → workload.
func augmentHTTPRoutes(ctx context.Context, gwc gatewayclient.Interface, cs kubernetes.Interface, groups map[AppKey]*appGroup) error {
	routes, err := gwc.GatewayV1().HTTPRoutes("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("listing httproutes: %w", err)
	}

	svcToApp := serviceIndex(groups)

	for i := range routes.Items {
		route := &routes.Items[i]
		hosts := extractHosts(route)

		// Resolve backendRefs to services, then to app groups
		for _, rule := range route.Spec.Rules {
			for _, ref := range rule.BackendRefs {
				if ref.Kind != nil && string(*ref.Kind) != "Service" {
					continue
				}
				svcName := string(ref.Name)
				ns := route.Namespace
				if ref.Namespace != nil {
					ns = string(*ref.Namespace)
				}

				appKey, ok := svcToApp[ns+"/"+svcName]
				if !ok {
					continue
				}

				g := groups[appKey]
				gateway := extractGateway(route)
				for _, h := range hosts {
					g.routes = append(g.routes, ExposureRef{
						Kind:    "HTTPRoute",
						Host:    h,
						Name:    route.Name,
						Gateway: gateway,
					})
				}
			}
		}
	}

	return nil
}

// serviceIndex maps "ns/service" to the app group that service already belongs to.
func serviceIndex(groups map[AppKey]*appGroup) map[string]AppKey {
	idx := map[string]AppKey{}
	for key, g := range groups {
		for _, svc := range g.services {
			idx[svc.Namespace+"/"+svc.Name] = key
		}
	}
	return idx
}

// augmentIngresses attaches Ingress hostnames to the apps whose services they route to.
func augmentIngresses(ctx context.Context, cs kubernetes.Interface, groups map[AppKey]*appGroup) error {
	ingresses, err := cs.NetworkingV1().Ingresses("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("listing ingresses: %w", err)
	}
	svcToApp := serviceIndex(groups)

	for i := range ingresses.Items {
		ing := &ingresses.Items[i]
		class := ""
		if ing.Spec.IngressClassName != nil {
			class = *ing.Spec.IngressClassName
		} else {
			class = ing.Annotations["kubernetes.io/ingress.class"]
		}

		var hosts []string
		seenHost := map[string]bool{}
		for _, r := range ing.Spec.Rules {
			if r.Host != "" && !seenHost[r.Host] {
				seenHost[r.Host] = true
				hosts = append(hosts, r.Host)
			}
		}

		var services []string
		seenSvc := map[string]bool{}
		addService := func(b *networkingv1.IngressBackend) {
			if b != nil && b.Service != nil && !seenSvc[b.Service.Name] {
				seenSvc[b.Service.Name] = true
				services = append(services, b.Service.Name)
			}
		}
		addService(ing.Spec.DefaultBackend)
		for _, r := range ing.Spec.Rules {
			if r.HTTP == nil {
				continue
			}
			for _, p := range r.HTTP.Paths {
				addService(&p.Backend)
			}
		}

		for _, svc := range services {
			key, ok := svcToApp[ing.Namespace+"/"+svc]
			if !ok {
				continue
			}
			g := groups[key]
			for _, h := range hosts {
				g.routes = append(g.routes, ExposureRef{Kind: "Ingress", Host: h, Name: ing.Name, Class: class})
			}
		}
	}
	return nil
}

// exposedService reports how an app's Services expose it: the most reachable type
// (LoadBalancer, then NodePort, then ClusterIP) and every ip:port its LoadBalancer
// Services serve, so service-type, endpoint and CIDR rules see what the app exposes.
// Services are read in name order, so the result is the same on every run.
func exposedService(services []corev1.Service) (string, []Endpoint) {
	rank := map[corev1.ServiceType]int{corev1.ServiceTypeLoadBalancer: 3, corev1.ServiceTypeNodePort: 2, corev1.ServiceTypeClusterIP: 1}
	sorted := append([]corev1.Service(nil), services...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	best := ""
	var endpoints []Endpoint
	for _, svc := range sorted {
		if best == "" || rank[svc.Spec.Type] > rank[corev1.ServiceType(best)] {
			best = string(svc.Spec.Type)
		}
		if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
			continue
		}
		var ips []string
		for _, in := range svc.Status.LoadBalancer.Ingress {
			if in.IP != "" {
				ips = append(ips, in.IP)
			}
		}
		if len(ips) == 0 && svc.Spec.LoadBalancerIP != "" {
			ips = append(ips, svc.Spec.LoadBalancerIP)
		}
		for _, ip := range ips {
			for _, p := range svc.Spec.Ports {
				endpoints = append(endpoints, Endpoint{IP: ip, Port: int(p.Port)})
			}
		}
	}
	return best, endpoints
}

// extractHosts returns unique hostnames from an HTTPRoute.
func extractHosts(route *gatewayv1.HTTPRoute) []string {
	seen := map[string]bool{}
	var hosts []string
	for _, h := range route.Spec.Hostnames {
		host := string(h)
		if !seen[host] {
			seen[host] = true
			hosts = append(hosts, host)
		}
	}
	return hosts
}

// extractGateway returns the first gateway parentRef name from an HTTPRoute.
func extractGateway(route *gatewayv1.HTTPRoute) string {
	for _, ref := range route.Spec.ParentRefs {
		if ref.Kind != nil && string(*ref.Kind) != "Gateway" {
			continue
		}
		return string(ref.Name)
	}
	return ""
}

// buildRecords converts app groups into classified AppRecords.
func buildRecords(groups map[AppKey]*appGroup, catalog *Catalog, resolver *CategoryResolver, fluxSources map[AppKey][]DeclaredSource, exposureRules config.ExposureRules) []AppRecord {
	var records []AppRecord

	keys := make([]AppKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].less(keys[j]) })

	for _, key := range keys {
		g := groups[key]
		rec := AppRecord{
			Key:       g.identity.Key,
			Category:  resolver.Resolve(g.identity.Key.Namespace),
			Collision: g.identity.Key.Identity != g.identity.Key.Identity, // always false; collision set in addWorkload
		}

		// FriendlyName: authority chain — labels → titleCase fallback.
		rec.FriendlyName = resolveFriendlyName(g)

		// Components + WorkloadKinds + Role classification
		kindSet := map[string]bool{}
		for _, wl := range g.workloads {
			images := extractContainerImageNames(wl.containers)
			role := ClassifyComponentRole(wl.name, images)
			rec.Components = append(rec.Components, ComponentRef{Name: wl.name, Kind: wl.kind, Role: role})
			kindSet[wl.kind] = true
		}
		for k := range kindSet {
			rec.WorkloadKinds = append(rec.WorkloadKinds, k)
		}
		sort.Strings(rec.WorkloadKinds)

		// Images + Version (exclude sidecars and initContainers)
		rec.Images, rec.Version = extractImagesAndVersion(g)

		// Hosts (deduplicated, sorted) + Exposure from gateway parentRefs
		rec.Hosts = dedupeHosts(g.routes)
		svcType, endpoints := exposedService(g.services)
		rec.Exposure = ClassifyExposure(g.routes, svcType, endpoints, exposureRules)

		// Replicas + Status
		var totalReady, totalDesired int32
		for _, wl := range g.workloads {
			totalReady += wl.ready
			totalDesired += wl.replicas
		}
		rec.Replicas = fmt.Sprintf("%d/%d", totalReady, totalDesired)
		rec.Status = ComputeStatus(totalReady, totalDesired)

		// Classification (before catalog, so catalog can override)
		rec.Tier = classify(g, rec, resolver.Tier(g.identity.Key.Namespace), resolver.Declared(g.identity.Key.Namespace))

		// Check label override
		for _, wl := range g.workloads {
			if tier, ok := wl.labels[labelTierOverride]; ok {
				rec.Tier = Tier(tier)
				break
			}
		}

		// Attach declared sources from Flux graph
		if srcs, ok := fluxSources[g.identity.Key]; ok {
			rec.Sources = srcs
		}

		// Apply catalog overrides (takes precedence)
		catalog.ApplyOverrides(&rec)

		records = append(records, rec)
	}

	return records
}

// extractImagesAndVersion processes containers, filtering sidecars and initContainers.
func extractImagesAndVersion(g *appGroup) ([]ImageRef, string) {
	seen := map[string]bool{}
	var images []ImageRef
	var tags []string

	for _, wl := range g.workloads {
		// Only main containers (not initContainers)
		for _, c := range wl.containers {
			if IsSidecarImage(c.Image) {
				continue
			}

			ref := parseImage(c.Image)
			key := ref.Repository + ":" + ref.Tag
			if !seen[key] {
				seen[key] = true
				images = append(images, ref)
				if ref.Tag != "" && ref.Tag != "latest" {
					tags = append(tags, ref.Tag)
				}
			}
		}
	}

	// Check version label first
	for _, wl := range g.workloads {
		if v, ok := wl.labels[labelVersion]; ok && v != "" {
			return images, v
		}
	}

	// Tag consensus: only if all non-sidecar tags are identical
	version := ""
	if len(tags) > 0 {
		allSame := true
		for _, t := range tags[1:] {
			if t != tags[0] {
				allSame = false
				break
			}
		}
		if allSame {
			version = tags[0]
		}
	}

	return images, version
}

// parseImage extracts repository and tag from a container image string.
// Handles: repo:tag, repo:tag@digest, repo (no tag → "latest"), malformed.
func parseImage(image string) ImageRef {
	// Strip digest
	if idx := strings.Index(image, "@"); idx > 0 {
		image = image[:idx]
	}

	// Split tag
	// Handle registry:port/repo:tag by finding last colon after last slash
	lastSlash := strings.LastIndex(image, "/")
	colonIdx := strings.LastIndex(image, ":")
	if colonIdx > lastSlash && lastSlash >= 0 {
		return ImageRef{Repository: image[:colonIdx], Tag: image[colonIdx+1:]}
	}
	if colonIdx > 0 && lastSlash < 0 {
		return ImageRef{Repository: image[:colonIdx], Tag: image[colonIdx+1:]}
	}

	return ImageRef{Repository: image, Tag: "latest"}
}

// classify resolves a workload's tier: declared, else inferred from generic Kubernetes
// signals, else platform in a namespace the cluster declares, else unresolved.
func classify(g *appGroup, rec AppRecord, declared Tier, categorized bool) Tier {
	// What the cluster declares for the namespace is authoritative; everything below
	// only fills gaps.
	if declared != "" {
		return declared
	}

	// Kubernetes' own namespaces
	switch g.identity.Key.Namespace {
	case "kube-system", "kube-node-lease", "kube-public":
		return TierPlatform
	}

	// Names that say what a workload does for the cluster rather than for its users
	name := strings.ToLower(g.identity.Key.Identity)
	for _, p := range []string{"operator", "controller", "exporter", "webhook", "provisioner", "csi-"} {
		if strings.Contains(name, p) {
			return TierPlatform
		}
	}

	// One pod per node serves the nodes
	allDaemonSet := true
	for _, wl := range g.workloads {
		if wl.kind != "DaemonSet" {
			allDaemonSet = false
			break
		}
	}
	if allDaemonSet {
		return TierPlatform
	}

	// Routed to users
	if len(rec.Hosts) > 0 {
		return TierApp
	}

	// The namespace is declared, so the workload is placed: one nobody is routed to
	// serves the apps beside it. Only a namespace the cluster says nothing about is
	// left unresolved rather than guessed.
	if categorized {
		return TierPlatform
	}
	return TierUnresolved
}

// selectorMatches returns true if selector ⊆ labels (strict subset).
func selectorMatches(selector, labels map[string]string) bool {
	if len(selector) == 0 {
		return false
	}
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// dedupeHosts extracts unique, sorted hostnames from exposure refs.
func dedupeHosts(routes []ExposureRef) []string {
	seen := map[string]bool{}
	var hosts []string
	for _, r := range routes {
		if r.Host != "" && !seen[r.Host] {
			seen[r.Host] = true
			hosts = append(hosts, r.Host)
		}
	}
	sort.Strings(hosts)
	return hosts
}

// resolveFluxSources walks the repo to discover Flux Kustomizations and matches
// them to apps by (namespace + identity). Returns sources keyed by AppKey.
// Uses gitops.DiscoverFluxGraph — local, proven, authoritative.
// Only includes authoritative matches — never guesses.
func resolveFluxSources(repoRoot string, groups map[AppKey]*appGroup) map[AppKey][]DeclaredSource {
	sources := map[AppKey][]DeclaredSource{}

	if repoRoot == "" {
		return sources
	}

	graph, err := gitops.DiscoverFluxGraph(repoRoot)
	if err != nil {
		return sources // can't discover, return empty (not fatal)
	}

	// Build app lookup: namespace → list of app identities
	type appRef struct {
		key      AppKey
		identity string
	}
	appsByNS := map[string][]appRef{}
	for key := range groups {
		appsByNS[key.Namespace] = append(appsByNS[key.Namespace], appRef{key: key, identity: key.Identity})
	}

	// Match Kustomizations to apps by (namespace + identity).
	// Identity match priority:
	//   1. Exact: kustomization.name == app.identity
	//   2. Path segment: last segment of spec.path == app.identity
	//   3. No match → no source (never guess)
	for kKey, kNode := range graph.Kustomizations {
		ns := kKey.Namespace
		apps, ok := appsByNS[ns]
		if !ok {
			continue
		}

		// Try to match this kustomization to an app
		pathSegment := lastPathSegment(kNode.Path)

		for _, app := range apps {
			matched := false
			// Priority 1: exact name match
			if strings.EqualFold(kKey.Name, app.identity) {
				matched = true
			}
			// Priority 2: path segment match
			if !matched && pathSegment != "" && strings.EqualFold(pathSegment, app.identity) {
				matched = true
			}

			if !matched {
				continue
			}

			// Authoritative match — add source
			src := DeclaredSource{
				Kind:     "kustomization",
				RepoPath: kNode.Path,
				Relation: SourceRelationDeploys,
				Primary:  true,
			}

			// Check for duplicates (same app might match multiple kustomizations)
			existing := sources[app.key]
			duplicate := false
			for _, e := range existing {
				if e.RepoPath == src.RepoPath {
					duplicate = true
					break
				}
			}
			if !duplicate {
				// If already has a primary, demote this one
				if len(existing) > 0 {
					src.Primary = false
					// Use shorter path as primary (usually the overlay root)
					if len(src.RepoPath) < len(existing[0].RepoPath) {
						existing[0].Primary = false
						src.Primary = true
					}
				}
				sources[app.key] = append(sources[app.key], src)
			}

			// Resolve dependsOn as secondary sources
			for _, dep := range kNode.DependsOn {
				depNode, ok := graph.Kustomizations[dep]
				if !ok || depNode.Path == "" {
					continue
				}
				depSrc := DeclaredSource{
					Kind:     "kustomization",
					RepoPath: depNode.Path,
					Relation: SourceRelationDependsOn,
				}
				sources[app.key] = append(sources[app.key], depSrc)
			}
		}
	}

	return sources
}

// lastPathSegment returns the last directory segment of a path.
func lastPathSegment(path string) string {
	path = strings.TrimRight(path, "/")
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		return path[idx+1:]
	}
	return path
}

// sortRecords sorts by declared category order then name (alpha, lowercase).
func sortRecords(records []AppRecord, resolver *CategoryResolver) {
	sort.SliceStable(records, func(i, j int) bool {
		if ci, cj := resolver.Order(records[i].Category), resolver.Order(records[j].Category); ci != cj {
			return ci < cj
		}
		if a, b := strings.ToLower(records[i].FriendlyName), strings.ToLower(records[j].FriendlyName); a != b {
			return a < b
		}
		// Equal display names (shared label value, catalog alias) are not equal records;
		// without a total order their relative position is whatever the input had.
		return records[i].Key.less(records[j].Key)
	})
}

// resolveFriendlyName derives the display name from authoritative metadata.
// Authority chain: app.kubernetes.io/name label → titleCase(identity) fallback.
// Catalog overrides apply later and take final precedence.
func resolveFriendlyName(g *appGroup) string {
	if name, ok := g.podLabels["app.kubernetes.io/name"]; ok && name != "" {
		return titleCase(name)
	}
	return titleCase(g.identity.Key.Identity)
}

// extractContainerImageNames returns the image name (no registry/tag) for each container.
func extractContainerImageNames(containers []corev1.Container) []string {
	names := make([]string, 0, len(containers))
	for _, c := range containers {
		if IsSidecarImage(c.Image) {
			continue
		}
		names = append(names, extractImageName(c.Image))
	}
	return names
}

// titleCase capitalizes the first letter of each word, handling hyphens.
func titleCase(s string) string {
	words := strings.FieldsFunc(s, func(c rune) bool { return c == '-' || c == '_' })
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// ptr32 dereferences an *int32 with a default value.
func ptr32(p *int32, def int32) int32 {
	if p != nil {
		return *p
	}
	return def
}
