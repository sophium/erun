package deploy

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery"
	memcached "k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// restConfig builds an in-memory Kubernetes REST config that addresses a
// provisioned cloud context's k3s API server directly — no kubeconfig file, no
// kubectl. It reproduces exactly what the provisioning kube-context setup did
// with `kubectl config set-cluster --insecure-skip-tls-verify` +
// `set-credentials --token`: the server is https://<publicIP>:6443, auth is the
// custodied k3s admin bearer token, and TLS verification is skipped (k3s serves
// a self-signed cert and the network path to the instance is the trust boundary).
func restConfig(publicIP, token string) *rest.Config {
	return &rest.Config{
		Host:            "https://" + publicIP + ":6443",
		BearerToken:     token,
		TLSClientConfig: rest.TLSClientConfig{Insecure: true},
		// A whole-request timeout so a half-open / black-holed API server cannot
		// stall the namespace-ensure or chart apply unbounded (client-go's dial +
		// TLS already cap quickly, but a peer that accepts then withholds response
		// headers would otherwise hang). QPS/Burst lift the conservative defaults
		// so a multi-object chart applies without client-side throttling.
		Timeout: 60 * time.Second,
		QPS:     50,
		Burst:   100,
	}
}

// restConfigGetter is the genericclioptions.RESTClientGetter the Helm SDK needs,
// backed by an in-memory *rest.Config. The stock genericclioptions.ConfigFlags
// cannot be used here: its ToRESTConfig reads a kubeconfig file and errors
// before any in-memory override applies, so in a pod with no kubeconfig file it
// fails. This getter returns the in-memory config directly instead.
type restConfigGetter struct {
	cfg       *rest.Config
	namespace string
}

// restConfigGetter must satisfy the interface the Helm SDK's action.Configuration
// .Init requires.
var _ genericclioptions.RESTClientGetter = (*restConfigGetter)(nil)

func (g *restConfigGetter) ToRESTConfig() (*rest.Config, error) {
	return g.cfg, nil
}

func (g *restConfigGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	dc, err := discovery.NewDiscoveryClientForConfig(g.cfg)
	if err != nil {
		return nil, err
	}
	return memcached.NewMemCacheClient(dc), nil
}

func (g *restConfigGetter) ToRESTMapper() (meta.RESTMapper, error) {
	dc, err := g.ToDiscoveryClient()
	if err != nil {
		return nil, err
	}
	return restmapper.NewDeferredDiscoveryRESTMapper(dc), nil
}

// ToRawKubeConfigLoader returns a client config whose namespace is the target
// namespace. This is load-bearing, not a no-op: Helm's kube client applies
// namespaceless rendered objects (the runtime chart does not hard-code
// metadata.namespace) to the namespace this loader reports, so without the
// override every object lands in "default" instead of <tenant>-<env>.
func (g *restConfigGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	overrides := &clientcmd.ConfigOverrides{Context: clientcmdapi.Context{Namespace: g.namespace}}
	return clientcmd.NewDefaultClientConfig(*clientcmdapi.NewConfig(), overrides)
}

// ensureNamespace creates the target namespace via client-go, treating an
// already-existing namespace as success. It replaces the previous
// `kubectl create namespace` subprocess (with its brittle stderr matching) with
// a typed API call.
func ensureNamespace(ctx context.Context, cfg *rest.Config, namespace string) error {
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return err
	}
	_, err = clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: namespace},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}
