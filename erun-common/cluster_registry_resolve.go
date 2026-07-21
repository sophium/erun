package eruncommon

import (
	"fmt"
	"os"
	"strings"
)

// RunningInCluster reports whether this process runs inside a Kubernetes pod —
// the remote-agent/runtime in-pod build case, where the registry is reached
// directly at its in-cluster address instead of through a host port-forward.
func RunningInCluster() bool {
	return strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_HOST")) != ""
}

// clusterRegistryHost joins a resolved host and the registry port.
func clusterRegistryHost(host string, port int) string {
	return fmt.Sprintf("%s:%d", strings.TrimSpace(host), port)
}

// ClusterRegistryDeps are the side effects the resolver needs, injected so the
// resolution logic stays testable and a dry-run can trace without touching the
// cluster.
type ClusterRegistryDeps struct {
	// InCluster selects the in-pod path: push and pull share the in-cluster
	// address, and no port-forward is started.
	InCluster bool
	// LookupClusterIP returns the ClusterIP of svc/<service> in <namespace> under
	// the given kube-context. The cluster pulls (and an in-pod build pushes) at
	// this address, which node containerd routes via kube-proxy.
	LookupClusterIP func(kubeContext, namespace, service string) (string, error)
	// StartPortForward forwards a local port to svc/<service>:<remotePort> and
	// returns the chosen local port. Only called for a host build.
	StartPortForward func(kubeContext, namespace, service string, remotePort int) (int, error)
}

// NewClusterRegistryResolver builds a ClusterRegistryResolver bound to a
// kube-context. The pull host is always the registry Service's ClusterIP; the
// push host is that same address in-pod, or a managed port-forward
// (localhost:<port>) on the host.
func NewClusterRegistryResolver(kubeContext string, deps ClusterRegistryDeps) ClusterRegistryResolver {
	return func(c ClusterRegistry) (ClusterRegistryAddresses, error) {
		c = c.WithDefaults()
		if deps.LookupClusterIP == nil {
			return ClusterRegistryAddresses{}, fmt.Errorf("cluster registry resolver missing a ClusterIP lookup")
		}
		clusterIP, err := deps.LookupClusterIP(kubeContext, c.Namespace, c.Service)
		if err != nil {
			return ClusterRegistryAddresses{}, fmt.Errorf("resolve cluster registry %s/%s: %w", c.Namespace, c.Service, err)
		}
		if strings.TrimSpace(clusterIP) == "" {
			return ClusterRegistryAddresses{}, fmt.Errorf("cluster registry %s/%s has no ClusterIP; create the registry Service first", c.Namespace, c.Service)
		}
		pull := clusterRegistryHost(clusterIP, c.Port)
		if deps.InCluster {
			return ClusterRegistryAddresses{Push: pull, Pull: pull}, nil
		}
		if deps.StartPortForward == nil {
			return ClusterRegistryAddresses{}, fmt.Errorf("cluster registry resolver missing a port-forward starter")
		}
		localPort, err := deps.StartPortForward(kubeContext, c.Namespace, c.Service, c.Port)
		if err != nil {
			return ClusterRegistryAddresses{}, fmt.Errorf("port-forward cluster registry %s/%s: %w", c.Namespace, c.Service, err)
		}
		return ClusterRegistryAddresses{Push: clusterRegistryHost("localhost", localPort), Pull: pull}, nil
	}
}
