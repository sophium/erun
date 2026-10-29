package kube

import (
	"fmt"
	"os"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
)

// RunningInCluster reports whether this process runs in a pod, where the registry is reached directly.
func RunningInCluster() bool {
	return strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_HOST")) != ""
}

func clusterRegistryHost(host string, port int) string {
	return fmt.Sprintf("%s:%d", strings.TrimSpace(host), port)
}

// ClusterRegistryDeps are the resolver's injected side effects.
type ClusterRegistryDeps struct {
	// InCluster shares the in-cluster address for push and pull, with no port-forward.
	InCluster bool
	// LookupClusterIP returns svc/<service>'s ClusterIP, which node containerd reaches via kube-proxy.
	LookupClusterIP func(kubeContext, namespace, service string) (string, error)
	// StartPortForward is only called for a host build.
	StartPortForward func(kubeContext, namespace, service string, remotePort int) (int, error)
}

// NewClusterRegistryResolver pulls via the Service ClusterIP; pushes use it in-pod or a
// localhost port-forward on the host.
func NewClusterRegistryResolver(kubeContext string, deps ClusterRegistryDeps) config.ClusterRegistryResolver {
	return func(c config.ClusterRegistry) (config.ClusterRegistryAddresses, error) {
		c = c.WithDefaults()
		if deps.LookupClusterIP == nil {
			return config.ClusterRegistryAddresses{}, fmt.Errorf("cluster registry resolver missing a ClusterIP lookup")
		}
		clusterIP, err := deps.LookupClusterIP(kubeContext, c.Namespace, c.Service)
		if err != nil {
			return config.ClusterRegistryAddresses{}, fmt.Errorf("resolve cluster registry %s/%s: %w", c.Namespace, c.Service, err)
		}
		if strings.TrimSpace(clusterIP) == "" {
			return config.ClusterRegistryAddresses{}, fmt.Errorf("cluster registry %s/%s has no ClusterIP; create the registry Service first", c.Namespace, c.Service)
		}
		pull := clusterRegistryHost(clusterIP, c.Port)
		if deps.InCluster {
			return config.ClusterRegistryAddresses{Push: pull, Pull: pull}, nil
		}
		if deps.StartPortForward == nil {
			return config.ClusterRegistryAddresses{}, fmt.Errorf("cluster registry resolver missing a port-forward starter")
		}
		localPort, err := deps.StartPortForward(kubeContext, c.Namespace, c.Service, c.Port)
		if err != nil {
			return config.ClusterRegistryAddresses{}, fmt.Errorf("port-forward cluster registry %s/%s: %w", c.Namespace, c.Service, err)
		}
		return config.ClusterRegistryAddresses{Push: clusterRegistryHost("localhost", localPort), Pull: pull}, nil
	}
}
