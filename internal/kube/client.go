// Package kube constructs Kubernetes clients and resolves kinds to
// resources via API discovery.
package kube

import (
	"fmt"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

// Clients bundles the API clients the tools need.
type Clients struct {
	Typed   kubernetes.Interface
	Dynamic dynamic.Interface
	Mapper  *Mapper
	// Metrics talks to the metrics.k8s.io API (metrics-server or an
	// adapter). Calls fail cleanly when the cluster doesn't serve it.
	Metrics metricsclient.Interface
}

// NewClients builds clients from the given kubeconfig path, or in-cluster
// config when the path is empty and the process runs in a pod, falling back
// to client-go's default loading rules ($KUBECONFIG, ~/.kube/config).
func NewClients(kubeconfig string) (*Clients, error) {
	cfg, err := restConfig(kubeconfig)
	if err != nil {
		return nil, err
	}
	typed, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("building typed client: %w", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("building dynamic client: %w", err)
	}
	disco, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("building discovery client: %w", err)
	}
	metrics, err := metricsclient.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("building metrics client: %w", err)
	}

	return &Clients{
		Typed:   typed,
		Dynamic: dyn,
		Mapper:  NewMapper(memory.NewMemCacheClient(disco)),
		Metrics: metrics,
	}, nil
}

func restConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig == "" {
		if cfg, err := rest.InClusterConfig(); err == nil {
			return cfg, nil
		}
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = kubeconfig
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, nil).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig: %w", err)
	}

	return cfg, nil
}
