package k8s

import (
	"context"
	"slices"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func service(name string, typ corev1.ServiceType, lbIP string) corev1.Service {
	svc := corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: name}, Spec: corev1.ServiceSpec{Type: typ, Ports: []corev1.ServicePort{{Port: 80}}}}
	if lbIP != "" {
		svc.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{IP: lbIP}}
	}
	return svc
}

func appGroupWith(name string, services ...corev1.Service) *appGroup {
	return &appGroup{
		identity:  WorkloadIdentity{Key: AppKey{Namespace: "shop", Identity: name}},
		workloads: []workloadInfo{{name: name, kind: "Deployment", labels: map[string]string{}}},
		services:  services,
	}
}

func TestIngressAttachesHostsAndClass(t *testing.T) {
	class := "nginx"
	pathType := networkingv1.PathTypePrefix
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "storefront"},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &class,
			Rules: []networkingv1.IngressRule{{
				Host: "shop.example.com",
				IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
					Paths: []networkingv1.HTTPIngressPath{{Path: "/", PathType: &pathType, Backend: networkingv1.IngressBackend{
						Service: &networkingv1.IngressServiceBackend{Name: "web", Port: networkingv1.ServiceBackendPort{Number: 80}},
					}}},
				}},
			}},
		},
	}
	key := AppKey{Namespace: "shop", Identity: "web"}
	groups := map[AppKey]*appGroup{key: appGroupWith("web", service("web", corev1.ServiceTypeClusterIP, ""))}

	if err := augmentIngresses(context.Background(), fake.NewSimpleClientset(ing), groups, config.GroupSource{}); err != nil {
		t.Fatal(err)
	}
	routes := groups[key].routes
	if len(routes) != 1 || routes[0].Host != "shop.example.com" || routes[0].Class != "nginx" || routes[0].Kind != "Ingress" {
		t.Fatalf("got %+v", routes)
	}
	rules := config.ExposureRules{Rules: []config.ExposureRule{{Level: "internet", IngressClasses: []string{"nginx"}}}}
	if got := ClassifyExposure(routes, "ClusterIP", nil, rules); got.Declared != ExposureInternet || got.Gateway != "nginx" {
		t.Errorf("an ingress class rule decides exposure: got %+v", got)
	}
}

// Service types, endpoints and CIDRs were never fed to classification, so those rules
// could not match. They now see the app's most reachable Service.
func TestServiceRulesSeeTheAppsServices(t *testing.T) {
	rules := config.ExposureRules{Rules: []config.ExposureRule{
		{Level: "cluster", ServiceTypes: []string{"ClusterIP"}},
		{Level: "intranet", ServiceTypes: []string{"NodePort"}},
		{Level: "intranet", CIDRs: []string{"10.0.40.0/24"}},
	}}
	groups := map[AppKey]*appGroup{}
	for _, g := range []*appGroup{
		appGroupWith("internal", service("internal", corev1.ServiceTypeClusterIP, "")),
		appGroupWith("printer", service("printer", corev1.ServiceTypeNodePort, "")),
		appGroupWith("media", service("media-api", corev1.ServiceTypeClusterIP, ""), service("media", corev1.ServiceTypeLoadBalancer, "10.0.40.7")),
	} {
		groups[g.identity.Key] = g
	}
	got := map[string]ExposureLevel{}
	for _, r := range buildRecords(groups, &Catalog{}, NewCategoryResolver(nil), nil, rules, config.GroupSource{}) {
		got[r.Key.Identity] = r.Exposure.Declared
	}
	want := map[string]ExposureLevel{"internal": ExposureCluster, "printer": ExposureIntranet, "media": ExposureIntranet}
	for name, level := range want {
		if got[name] != level {
			t.Errorf("%s: got %q, want %q", name, got[name], level)
		}
	}
}

func TestExposedServiceListsEveryLoadBalancedPort(t *testing.T) {
	lb := service("z", corev1.ServiceTypeLoadBalancer, "192.0.2.10")
	lb.Spec.Ports = []corev1.ServicePort{{Port: 8211}, {Port: 27015}}
	typ, eps := exposedService([]corev1.Service{service("a", corev1.ServiceTypeClusterIP, ""), lb, service("m", corev1.ServiceTypeNodePort, "")})
	want := []Endpoint{{"192.0.2.10", 8211}, {"192.0.2.10", 27015}}
	if typ != "LoadBalancer" || !slices.Equal(eps, want) {
		t.Errorf("got %s %v", typ, eps)
	}
}

// A shared load-balancer address with one port forwarded: only the app behind that port
// is internet-facing, the rest of the address stays on the LAN.
func TestForwardedPortIsExactOnASharedAddress(t *testing.T) {
	rules := config.ExposureRules{Rules: []config.ExposureRule{
		{Level: "internet", Endpoints: []string{"192.0.2.10:35566"}},
		{Level: "intranet", CIDRs: []string{"192.0.2.0/24"}},
	}}
	game := []Endpoint{{"192.0.2.10", 35566}}
	admin := []Endpoint{{"192.0.2.10", 28019}}
	if got := ClassifyExposure(nil, "LoadBalancer", game, rules); got.Declared != ExposureInternet || got.Endpoint != "192.0.2.10:35566" {
		t.Errorf("forwarded port: got %+v", got)
	}
	if got := ClassifyExposure(nil, "LoadBalancer", admin, rules); got.Declared != ExposureIntranet {
		t.Errorf("same address, unforwarded port: got %+v", got)
	}
	ported := config.ExposureRules{Rules: []config.ExposureRule{{Level: "internet", CIDRs: []string{"192.0.2.0/24"}, Ports: []int{35566}}}}
	if got := ClassifyExposure(nil, "LoadBalancer", admin, ported); got.Declared == ExposureInternet {
		t.Errorf("a CIDR rule's ports narrow it: got %+v", got)
	}
}
