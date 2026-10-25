package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config/containerregistry"
	"github.com/sophium/erun/erun-common/base/config/execmode"
	"github.com/sophium/erun/erun-common/base/core"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// kubectlNamespaceGetExecutionOperation switches the namespace existence check run before helm deploys.
const kubectlNamespaceGetExecutionOperation = "kubectl-namespace-get"

// kubectlGetNamespaceArgs is shared by the trace and the subprocess path.
func kubectlGetNamespaceArgs(contextName, namespace string) []string {
	args := kubernetesContextArgs(contextName)
	return append(args, "get", "namespace", strings.TrimSpace(namespace), "-o", "name")
}

// libraryKubernetesNamespaceExists answers via clientcmd, the kubeconfig loader kubectl itself uses.
func libraryKubernetesNamespaceExists(contextName, namespace string) (bool, error) {
	contextName = strings.TrimSpace(contextName)
	namespace = strings.TrimSpace(namespace)
	clientset, err := KubernetesClientsetForContext(contextName)
	if err != nil {
		return false, fmt.Errorf("failed to check kubernetes namespace %q in context %q: %w", namespace, contextName, err)
	}
	_, err = clientset.CoreV1().Namespaces().Get(context.Background(), namespace, metav1.GetOptions{})
	if err == nil {
		return true, nil
	}
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("failed to check kubernetes namespace %q in context %q: %w", namespace, contextName, err)
}

// kubernetesClientConfigLoader is kubectl's non-interactive loader, falling back to the
// in-cluster service account when no kubeconfig names a current context.
func kubernetesClientConfigLoader(contextName string) clientcmd.ClientConfig {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{}
	if contextName = strings.TrimSpace(contextName); contextName != "" {
		overrides.CurrentContext = contextName
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)
}

// KubernetesClientsetForContext builds a typed clientset from the ambient kubeconfig.
func KubernetesClientsetForContext(contextName string) (*kubernetes.Clientset, error) {
	config, err := kubernetesClientConfigLoader(contextName).ClientConfig()
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(config)
}

// KubectlPVCGetExecutionOperation switches the worktree PVC existence check.
const KubectlPVCGetExecutionOperation = "kubectl-pvc-get"

// LibraryPersistentVolumeClaimExists checks PVC existence via client-go.
func LibraryPersistentVolumeClaimExists(contextName, namespace, claim string) (bool, error) {
	contextName = strings.TrimSpace(contextName)
	namespace = strings.TrimSpace(namespace)
	claim = strings.TrimSpace(claim)
	clientset, err := KubernetesClientsetForContext(contextName)
	if err != nil {
		return false, fmt.Errorf("failed to check persistent volume claim %q in namespace %q context %q: %w", claim, namespace, contextName, err)
	}
	_, err = clientset.CoreV1().PersistentVolumeClaims(namespace).Get(context.Background(), claim, metav1.GetOptions{})
	if err == nil {
		return true, nil
	}
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("failed to check persistent volume claim %q in namespace %q context %q: %w", claim, namespace, contextName, err)
}

// LibraryImagePullSecretAuths needs no base64 decode: a typed Secret's Data is already decoded.
func LibraryImagePullSecretAuths(contextName, namespace, name string) (map[string]containerregistry.DockerConfigJSONAuthEntry, error) {
	contextName = strings.TrimSpace(contextName)
	namespace = strings.TrimSpace(namespace)
	name = strings.TrimSpace(name)
	clientset, err := KubernetesClientsetForContext(contextName)
	if err != nil {
		return nil, fmt.Errorf("read existing image pull secret %s: %w", name, err)
	}
	secret, err := clientset.CoreV1().Secrets(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil //nolint:nilnil // a nil map is the empty auths set: no existing coverage
		}
		return nil, fmt.Errorf("read existing image pull secret %s: %w", name, err)
	}
	encoded, ok := secret.Data[containerregistry.DockerConfigJSONSecretKey]
	if !ok {
		return nil, nil //nolint:nilnil // a nil map is the empty auths set: no existing coverage
	}
	var file containerregistry.DockerConfigJSONFile
	if err := json.Unmarshal(encoded, &file); err != nil {
		return nil, fmt.Errorf("read existing image pull secret %s: %s does not decode as a docker config", name, containerregistry.DockerConfigJSONSecretKey)
	}
	return file.Auths, nil
}

// KubectlPodGetExecutionOperation switches the pod self-reads; callers run inside the pod
// they query, so context and namespace resolve as kubectl would without flags.
const KubectlPodGetExecutionOperation = "kubectl-pod-get"

// KubectlGetPodArgs is shared by both subprocess call sites.
func KubectlGetPodArgs(podName string) []string {
	return []string{"get", "pod", strings.TrimSpace(podName), "-o", "json"}
}

// kubectlDeploymentGetExecutionOperation switches the bare deployment existence check
// before an API port-forward; richer deployment reads stay subprocess-only.
const kubectlDeploymentGetExecutionOperation = "kubectl-deployment-get"

// KubectlGetDeploymentArgs is shared by the trace and the subprocess path.
func KubectlGetDeploymentArgs(contextName, namespace, name string) []string {
	args := make([]string, 0, 7)
	if contextName = strings.TrimSpace(contextName); contextName != "" {
		args = append(args, "--context", contextName)
	}
	if namespace = strings.TrimSpace(namespace); namespace != "" {
		args = append(args, "--namespace", namespace)
	}
	return append(args, "get", "deployment", strings.TrimSpace(name), "-o", "name")
}

// DeploymentPresent distinguishes "not there" from "unknown" so an unreadable cluster is never "absent".
func DeploymentPresent(contextName, namespace, name string) (bool, error) {
	if execmode.CurrentExecutionMode(kubectlDeploymentGetExecutionOperation) == execmode.ExecutionModeLibrary {
		return libraryDeploymentPresent(contextName, namespace, name)
	}
	return defaultDeploymentPresent(contextName, namespace, name)
}

func defaultDeploymentPresent(contextName, namespace, name string) (bool, error) {
	output, err := core.Command("kubectl", KubectlGetDeploymentArgs(contextName, namespace, name)...).CombinedOutput()
	if err == nil {
		return true, nil
	}
	message := strings.ToLower(strings.TrimSpace(string(output)))
	if strings.Contains(message, "notfound") || strings.Contains(message, "not found") || strings.Contains(message, "no resources found") {
		return false, nil
	}
	return false, fmt.Errorf("failed to check deployment %q in namespace %q context %q: %w: %s", name, namespace, contextName, err, strings.TrimSpace(string(output)))
}

func libraryDeploymentPresent(contextName, namespace, name string) (bool, error) {
	contextName = strings.TrimSpace(contextName)
	namespace = strings.TrimSpace(namespace)
	name = strings.TrimSpace(name)
	clientset, err := KubernetesClientsetForContext(contextName)
	if err != nil {
		return false, fmt.Errorf("failed to check deployment %q in namespace %q context %q: %w", name, namespace, contextName, err)
	}
	_, err = clientset.AppsV1().Deployments(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err == nil {
		return true, nil
	}
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("failed to check deployment %q in namespace %q context %q: %w", name, namespace, contextName, err)
}

// LibraryGetPod resolves the namespace as kubectl does with no --namespace: kubeconfig
// context, else the mounted service account's namespace.
func LibraryGetPod(podName string) (*corev1.Pod, error) {
	podName = strings.TrimSpace(podName)
	loader := kubernetesClientConfigLoader("")
	namespace, _, err := loader.Namespace()
	if err != nil {
		return nil, fmt.Errorf("read pod %q: resolve current namespace: %w", podName, err)
	}
	config, err := loader.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("read pod %q: %w", podName, err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("read pod %q: %w", podName, err)
	}
	pod, err := clientset.CoreV1().Pods(namespace).Get(context.Background(), podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("read pod %q in namespace %q: %w", podName, namespace, err)
	}
	return pod, nil
}

// KubectlDeploymentWaitExecutionOperation switches `kubectl wait --for=condition=Available`.
// The library path polls Get rather than using a watch, like watchReleasePods.
const KubectlDeploymentWaitExecutionOperation = "kubectl-deployment-wait"

// defaultDeploymentWaitPollInterval is overridable via ERUN_KUBECTL_DEPLOYMENT_WAIT_POLL_INTERVAL for tests.
const defaultDeploymentWaitPollInterval = 500 * time.Millisecond

func resolveDeploymentWaitPollInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv("ERUN_KUBECTL_DEPLOYMENT_WAIT_POLL_INTERVAL"))
	if raw == "" {
		return defaultDeploymentWaitPollInterval
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return defaultDeploymentWaitPollInterval
	}
	return parsed
}

// LibraryWaitForDeploymentAvailable takes timeout in kubectl's --timeout duration format.
func LibraryWaitForDeploymentAvailable(contextName, namespace, name, timeout string) error {
	contextName = strings.TrimSpace(contextName)
	namespace = strings.TrimSpace(namespace)
	name = strings.TrimSpace(name)
	duration, err := time.ParseDuration(strings.TrimSpace(timeout))
	if err != nil {
		return fmt.Errorf("failed to wait for deployment %q in namespace %q context %q: invalid timeout %q: %w", name, namespace, contextName, timeout, err)
	}
	clientset, err := KubernetesClientsetForContext(contextName)
	if err != nil {
		return fmt.Errorf("failed to wait for deployment %q in namespace %q context %q: %w", name, namespace, contextName, err)
	}

	deployments := clientset.AppsV1().Deployments(namespace)
	interval := resolveDeploymentWaitPollInterval()
	deadline := time.Now().Add(duration)
	for {
		deployment, err := deployments.Get(context.Background(), name, metav1.GetOptions{})
		switch {
		case err == nil && deploymentConditionAvailable(deployment):
			return nil
		case err != nil && !apierrors.IsNotFound(err):
			return fmt.Errorf("failed to wait for deployment %q in namespace %q context %q: %w", name, namespace, contextName, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for deployment %q in namespace %q context %q to report condition Available after %s", name, namespace, contextName, duration)
		}
		time.Sleep(interval)
	}
}

func deploymentConditionAvailable(deployment *appsv1.Deployment) bool {
	for _, condition := range deployment.Status.Conditions {
		if condition.Type == appsv1.DeploymentAvailable {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}
