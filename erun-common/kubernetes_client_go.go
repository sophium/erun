package eruncommon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// kubectlNamespaceGetExecutionOperation is the ExecutionModeFor/
// ExecutionModeReport key for the `kubectl get namespace <name> -o name`
// existence check (kubernetesNamespaceExists below), which
// EnsureKubernetesNamespace runs ahead of nearly every helm deploy
// (WrapHelmChartDeployerWithNamespaceEnsure). It is the first kubectl
// operation promoted onto the switch, chosen deliberately narrow: it is a
// single resource kind (Namespace), a single Get-by-name call, and it sits
// outside the helm deploy pod-watch loop (runHelmDeployWithPodWatch) that
// stays subprocess-only, per execution_mode.go's one-key-per-call-site
// convention. Every other kubectl call site — pod-watch polling, `apply`,
// `wait` — stays subprocess-only for now: this is the first use of
// k8s.io/client-go in the module, and paying that dependency cost to prove
// the approach on the narrowest, lowest-blast-radius read is preferable to
// spending it on the higher-traffic but streaming/mutating call sites in the
// same pass.
const kubectlNamespaceGetExecutionOperation = "kubectl-namespace-get"

// kubectlGetNamespaceArgs is the single source of the `kubectl get namespace
// <name> -o name` argv, shared by TraceEnsureKubernetesNamespace (which
// renders it for both dry-run and audit purposes) and
// defaultKubernetesNamespaceExists (which also executes it as a subprocess),
// so the dry-run trace can never drift from either execution path.
func kubectlGetNamespaceArgs(contextName, namespace string) []string {
	args := kubernetesContextArgs(contextName)
	return append(args, "get", "namespace", strings.TrimSpace(namespace), "-o", "name")
}

// libraryKubernetesNamespaceExists is the library-backed alternative to
// defaultKubernetesNamespaceExists. It resolves the same question — does this
// namespace exist in this context — via k8s.io/client-go's clientcmd loader
// instead of shelling out to kubectl. clientcmd is the same kubeconfig
// resolution library kubectl itself is built on (KUBECONFIG env, the default
// ~/.kube/config path, --context override, cluster/user merge, exec-plugin
// credential providers), so this does not reimplement kubectl's config
// resolution — it reuses it.
func libraryKubernetesNamespaceExists(contextName, namespace string) (bool, error) {
	contextName = strings.TrimSpace(contextName)
	namespace = strings.TrimSpace(namespace)
	clientset, err := kubernetesClientsetForContext(contextName)
	if err != nil {
		return false, fmt.Errorf("failed to check kubernetes namespace %q in context %q: %s", namespace, contextName, err)
	}
	_, err = clientset.CoreV1().Namespaces().Get(context.Background(), namespace, metav1.GetOptions{})
	if err == nil {
		return true, nil
	}
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("failed to check kubernetes namespace %q in context %q: %s", namespace, contextName, err)
}

// kubernetesClientConfigLoader builds the same non-interactive deferred
// loader kubectl itself resolves a kubeconfig through -- KUBECONFIG env var,
// --context override, and (when no kubeconfig names a usable current
// context) a fallback to the in-cluster service account. Shared by
// kubernetesClientsetForContext (the rest config) and libraryGetPod (which
// also needs the loader's resolved current namespace).
func kubernetesClientConfigLoader(contextName string) clientcmd.ClientConfig {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{}
	if contextName = strings.TrimSpace(contextName); contextName != "" {
		overrides.CurrentContext = contextName
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)
}

// kubernetesClientsetForContext builds a typed Kubernetes clientset from the
// ambient kubeconfig, honoring the same KUBECONFIG env var and --context
// override kubectl itself honors, via clientcmd's own non-interactive
// deferred loader.
func kubernetesClientsetForContext(contextName string) (*kubernetes.Clientset, error) {
	config, err := kubernetesClientConfigLoader(contextName).ClientConfig()
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(config)
}

// kubectlPVCGetExecutionOperation is the ExecutionModeFor/ExecutionModeReport
// key for the `kubectl get pvc <claim> -o name` existence check
// (worktreeClaimExists in deploy_worktree_adoption.go), which
// announceWorktreeVolumeChange runs ahead of a runtime-release deploy to
// decide whether the dedicated worktree volume already exists. Picked next
// after kubectl-namespace-get for the identical reason: a single resource
// kind, a single Get-by-name call, no streaming, no mutation — reusing
// kubernetesClientsetForContext rather than adding new dependency surface.
const kubectlPVCGetExecutionOperation = "kubectl-pvc-get"

// libraryPersistentVolumeClaimExists is the library-backed alternative to
// defaultWorktreeClaimExists, resolving the same existence question via
// k8s.io/client-go instead of shelling out to kubectl.
func libraryPersistentVolumeClaimExists(contextName, namespace, claim string) (bool, error) {
	contextName = strings.TrimSpace(contextName)
	namespace = strings.TrimSpace(namespace)
	claim = strings.TrimSpace(claim)
	clientset, err := kubernetesClientsetForContext(contextName)
	if err != nil {
		return false, fmt.Errorf("failed to check persistent volume claim %q in namespace %q context %q: %s", claim, namespace, contextName, err)
	}
	_, err = clientset.CoreV1().PersistentVolumeClaims(namespace).Get(context.Background(), claim, metav1.GetOptions{})
	if err == nil {
		return true, nil
	}
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("failed to check persistent volume claim %q in namespace %q context %q: %s", claim, namespace, contextName, err)
}

// libraryImagePullSecretAuths is the library-backed alternative to
// defaultExistingImagePullSecretAuths, reading the same dockerconfigjson
// Secret via k8s.io/client-go instead of shelling out to kubectl. A typed
// Secret's Data field is already base64-decoded by the API machinery (Go's
// json package decodes a []byte field from a base64 string automatically),
// so this needs no separate decode step the subprocess path's manual JSON
// parse does.
func libraryImagePullSecretAuths(contextName, namespace, name string) (map[string]dockerConfigJSONAuthEntry, error) {
	contextName = strings.TrimSpace(contextName)
	namespace = strings.TrimSpace(namespace)
	name = strings.TrimSpace(name)
	clientset, err := kubernetesClientsetForContext(contextName)
	if err != nil {
		return nil, fmt.Errorf("read existing image pull secret %s: %s", name, err)
	}
	secret, err := clientset.CoreV1().Secrets(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read existing image pull secret %s: %s", name, err)
	}
	encoded, ok := secret.Data[dockerConfigJSONSecretKey]
	if !ok {
		return nil, nil
	}
	var file dockerConfigJSONFile
	if err := json.Unmarshal(encoded, &file); err != nil {
		return nil, fmt.Errorf("read existing image pull secret %s: %s does not decode as a docker config", name, dockerConfigJSONSecretKey)
	}
	return file.Auths, nil
}

// kubectlPodGetExecutionOperation is the ExecutionModeFor/ExecutionModeReport
// key for the `kubectl get pod <name> -o json` self-check two existing call
// sites already ran through the subprocess path: jobSupervisorContainerRestart
// (job_container_restart.go), which reads this pod's own container status to
// attribute a vanished job supervisor to a real container restart instead of
// guessing pod replacement, and dockerBuildContainerMemoryLimit
// (build_resource_exhaustion.go), which reads this pod's own spec to name the
// configured memory limit behind an OOM-killed docker build. Picked next
// after kubectl-secret-get for the identical narrow shape -- a single
// resource kind (Pod), a single Get-by-name call, no streaming, no mutation
// -- and because porting it here folds both call sites onto one library seam
// instead of leaving them duplicated. Neither caller passes its own context
// or namespace: both run inside the pod they are asking about, so
// libraryGetPod resolves both the same way kubectl itself would with no
// --context/--namespace flag -- clientcmd's DeferredLoadingClientConfig falls
// back to the in-cluster service account when no kubeconfig names a current
// context.
const kubectlPodGetExecutionOperation = "kubectl-pod-get"

// kubectlGetPodArgs is the single source of the `kubectl get pod <name> -o
// json` argv, shared by both subprocess call sites so neither can drift from
// the other or from the library path's equivalent Get.
func kubectlGetPodArgs(podName string) []string {
	return []string{"get", "pod", strings.TrimSpace(podName), "-o", "json"}
}

// libraryGetPod is the library-backed alternative to shelling out to
// `kubectl get pod <name> -o json`, resolving the same pod via k8s.io/client-go
// instead. It takes no context or namespace: both current call sites read
// this pod's own status about itself, so the namespace is resolved the same
// way kubectl resolves it with no --namespace flag -- an explicit
// kubeconfig context's namespace, or (in a runtime pod with no kubeconfig at
// all) the namespace bound to the mounted service account token.
func libraryGetPod(podName string) (*corev1.Pod, error) {
	podName = strings.TrimSpace(podName)
	loader := kubernetesClientConfigLoader("")
	namespace, _, err := loader.Namespace()
	if err != nil {
		return nil, fmt.Errorf("read pod %q: resolve current namespace: %s", podName, err)
	}
	config, err := loader.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("read pod %q: %s", podName, err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("read pod %q: %s", podName, err)
	}
	pod, err := clientset.CoreV1().Pods(namespace).Get(context.Background(), podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("read pod %q in namespace %q: %s", podName, namespace, err)
	}
	return pod, nil
}
