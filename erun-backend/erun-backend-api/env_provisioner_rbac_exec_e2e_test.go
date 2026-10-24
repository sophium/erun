package backendapi

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// TestProvisionerPlatformRoleGrantsPowerDNSExec proves the namespaced
// env-provisioner-platform Role's pods/exec grant against a real RBAC
// engine: `erun expose`'s DNS write shells out to `kubectl exec` against the
// platform's PowerDNS pod, and that call must succeed under the current
// rules and be Forbidden without the grant.
func TestProvisionerPlatformRoleGrantsPowerDNSExec(t *testing.T) {
	if os.Getenv("ERUN_E2E_PROVISIONER_RBAC") != "1" {
		t.Skip("opt-in: set ERUN_E2E_PROVISIONER_RBAC=1 on a live cluster where this identity may create Pods/ServiceAccounts/Roles/RoleBindings and impersonate, to prove the env-provisioner-platform Role's pods/exec grant against a real RBAC engine")
	}
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm is required to render the chart under test")
	}

	tenant := fmt.Sprintf("rbacprobe%d", time.Now().UnixNano())
	fixedRules := renderProvisionerPlatformRoleRules(t, tenant)
	preFixRules := withoutPodsExecGrant(fixedRules)

	adminConfig := provisionerRBACConfig(t)
	namespace := provisionerRBACNamespace(t)
	admin, err := kubernetes.NewForConfig(adminConfig)
	mustNoErr(t, err, "build admin kube client")

	image := provisionerRBACProbeImage(t, admin, namespace)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	pod := createProbePod(t, admin, namespace, "erun-1089-exec-probe-"+suffix, image)
	waitForPodRunning(t, admin, namespace, pod.Name)

	_, fixedConfig := provisionerRBACIdentityWithConfig(t, admin, adminConfig, namespace, "erun-1089-exec-fixed-"+suffix, fixedRules)
	_, preFixConfig := provisionerRBACIdentityWithConfig(t, admin, adminConfig, namespace, "erun-1089-exec-prefix-"+suffix, preFixRules)

	assertExecForbiddenUnderPreFixRules(t, preFixConfig, namespace, pod.Name)
	assertExecSucceedsUnderFixedRules(t, fixedConfig, namespace, pod.Name)
}

// createProbePod stands up a real, minimal Pod -- standing in for the
// platform's PowerDNS pod -- for the exec probe under test to run against.
func createProbePod(t *testing.T, admin kubernetes.Interface, namespace, name, image string) *corev1.Pod {
	t.Helper()
	spec := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:    "probe",
				Image:   image,
				Command: []string{"sleep", "3600"},
			}},
		},
	}
	created, err := admin.CoreV1().Pods(namespace).Create(context.Background(), spec, metav1.CreateOptions{})
	mustNoErr(t, err, "create probe Pod")
	t.Cleanup(func() {
		_ = admin.CoreV1().Pods(namespace).Delete(context.Background(), created.Name, metav1.DeleteOptions{})
	})
	return created
}

// waitForPodRunning polls the admin client -- unaffected by anything under
// test -- until the probe Pod is actually running and execable.
func waitForPodRunning(t *testing.T, admin kubernetes.Interface, namespace, name string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		pod, err := admin.CoreV1().Pods(namespace).Get(context.Background(), name, metav1.GetOptions{})
		mustNoErr(t, err, "get probe Pod")
		if pod.Status.Phase == corev1.PodRunning {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("probe Pod %s/%s never became Running (phase: %s)", namespace, name, pod.Status.Phase)
		}
		time.Sleep(2 * time.Second)
	}
}

// assertExecForbiddenUnderPreFixRules asserts the broken state:
// `kubectl exec` => Forbidden for lack of the pods/exec grant.
func assertExecForbiddenUnderPreFixRules(t *testing.T, config *rest.Config, namespace, podName string) {
	t.Helper()
	err := execProbeCommand(config, namespace, podName)
	if err == nil {
		t.Fatal("exec into the probe pod succeeded under the pre-fix rule set (no pods/exec grant) -- either the premise of this test is wrong or this cluster's RBAC is not enforcing the rule set this test built")
	}
	if !apierrors.IsForbidden(err) {
		t.Fatalf("exec into the probe pod under the pre-fix rule set failed with %v, want a Forbidden error", err)
	}
	t.Logf("confirmed pre-fix rules: pods/exec => Forbidden (%v)", err)
}

// assertExecSucceedsUnderFixedRules performs the exact call `erun expose`'s
// PowerDNS write makes.
func assertExecSucceedsUnderFixedRules(t *testing.T, config *rest.Config, namespace, podName string) {
	t.Helper()
	err := execProbeCommand(config, namespace, podName)
	mustNoErr(t, err, "exec into the probe pod as the fixed provisioner identity")
	t.Logf("confirmed current rules: pods/exec => succeeded")
}

// execProbeCommand runs the exact call `erun expose`'s pdnsutil write makes
// (command/expose's upsertPowerDNSRecord): a `kubectl exec`
// against a real pod, negotiating the same SPDY protocol upgrade kubectl
// itself uses, so an RBAC denial surfaces the same Forbidden a real
// replace-rrset write would hit.
func execProbeCommand(config *rest.Config, namespace, podName string) error {
	execConfig := rest.CopyConfig(config)
	execConfig.APIPath = "/api"
	execConfig.GroupVersion = &corev1.SchemeGroupVersion
	execConfig.NegotiatedSerializer = scheme.Codecs.WithoutConversion()

	restClient, err := rest.RESTClientFor(execConfig)
	if err != nil {
		return err
	}
	req := restClient.Post().
		Resource("pods").
		Namespace(namespace).
		Name(podName).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command: []string{"true"},
			Stdout:  true,
			Stderr:  true,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(execConfig, "POST", req.URL())
	if err != nil {
		return err
	}
	var stdout, stderr bytes.Buffer
	return executor.StreamWithContext(context.Background(), remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
}
