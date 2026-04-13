package k8s

import (
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitops"
	"github.com/PrPlanIT/StageFreight/src/runtime"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	gatewayclient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"
)

// Client holds resolved Kubernetes API clients and cluster metadata.
// Created via NewClient — the only way to get authenticated cluster access.
// All auth resolution happens here. Consumers never build their own config.
// Exposure rules are NOT part of Client — they are discovery input, not bootstrap state.
type Client struct {
	Config        *rest.Config
	Clientset     kubernetes.Interface
	GatewayClient gatewayclient.Interface
	ClusterName   string // resolved from actual auth path, not blindly from input
	cleanup       func()
}

// NewClient resolves kubeconfig and builds authenticated API clients.
// Auth chain: default kubeconfig → OIDC BuildKubeconfig → in-cluster.
// ClusterName comes from the resolved context/path, not input config.
func NewClient(cluster config.ClusterConfig) (*Client, error) {
	cfg, clusterName, cleanup, err := resolveConfig(cluster)
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("creating kubernetes client: %w", err)
	}

	gwClient, err := gatewayclient.NewForConfig(cfg)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("creating gateway-api client: %w", err)
	}

	return &Client{
		Config:        cfg,
		Clientset:     clientset,
		GatewayClient: gwClient,
		ClusterName:   clusterName,
		cleanup:       cleanup,
	}, nil
}

// Close releases any resources (temp kubeconfig files from OIDC path).
func (c *Client) Close() {
	if c.cleanup != nil {
		c.cleanup()
	}
}

// resolveConfig implements the auth chain:
// 1. Default kubeconfig (~/.kube/config, KUBECONFIG env)
// 2. OIDC BuildKubeconfig (CI — only when step 1 fails and cluster config available)
// 3. In-cluster ServiceAccount token
func resolveConfig(cluster config.ClusterConfig) (*rest.Config, string, func(), error) {
	noop := func() {}

	// 1. Default kubeconfig — local dev, already configured.
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{})
	if cfg, err := kubeConfig.ClientConfig(); err == nil {
		rawConfig, _ := kubeConfig.RawConfig()
		return cfg, rawConfig.CurrentContext, noop, nil
	}

	// 2. OIDC BuildKubeconfig — CI path, writes temp kubeconfig then re-reads.
	if cluster.Name != "" {
		rctx := &runtime.RuntimeContext{}
		if err := gitops.BuildKubeconfig(cluster, rctx, nil); err == nil {
			// Re-read kubeconfig after OIDC setup wrote it.
			rules2 := clientcmd.NewDefaultClientConfigLoadingRules()
			kc2 := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules2, &clientcmd.ConfigOverrides{})
			if cfg, err := kc2.ClientConfig(); err == nil {
				return cfg, cluster.Name, rctx.Resolved.Cleanup, nil
			}
		}
	}

	// 3. In-cluster ServiceAccount token.
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, "in-cluster", noop, nil
	}

	return nil, "", noop, fmt.Errorf("no kubeconfig available (tried: default, OIDC, in-cluster)")
}
