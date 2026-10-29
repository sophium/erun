package backendapi

// Proves the provisioner's grants against a real RBAC engine, since helm's
// readiness wait walks an object graph only a real authorizer can judge. Opt-in:
// needs a live cluster where this identity may create roles and impersonate.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const provisionerRBACChartDir = "../../erun-devops/k8s/erun-backend-api"

func TestProvisionerClusterRoleGrantsHelmReadinessRead(t *testing.T) {
	if os.Getenv("ERUN_E2E_PROVISIONER_RBAC") != "1" {
		t.Skip("opt-in: set ERUN_E2E_PROVISIONER_RBAC=1 on a live cluster where this identity may create Roles/ServiceAccounts/RoleBindings and impersonate, to prove the provisioner ClusterRole's replicasets grant against a real RBAC engine (#1083)")
	}
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm is required to render the chart under test")
	}

	tenant := fmt.Sprintf("rbacprobe%d", time.Now().UnixNano())
	// Only the readiness grants: the probe identity cannot self-escalate into the rest.
	fixedRules := readinessRelevantRules(renderProvisionerClusterRoleRules(t, tenant))
	preFixRules := withoutReplicasetsGrant(fixedRules)

	adminConfig := provisionerRBACConfig(t)
	namespace := provisionerRBACNamespace(t)
	admin, err := kubernetes.NewForConfig(adminConfig)
	mustNoErr(t, err, "build admin kube client")

	image := provisionerRBACProbeImage(t, admin, namespace)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	deployment := createProbeDeployment(t, admin, namespace, "erun-1083-probe-"+suffix, image)

	fixed := provisionerRBACIdentity(t, admin, adminConfig, namespace, "erun-1083-fixed-"+suffix, fixedRules)
	preFix := provisionerRBACIdentity(t, admin, adminConfig, namespace, "erun-1083-prefix-"+suffix, preFixRules)

	deployment = waitForDeploymentReady(t, admin, namespace, deployment.Name)
	assertReplicaSetsForbiddenUnderPreFixRules(t, preFix, namespace)
	assertReplicaSetsObservableUnderFixedRules(t, fixed, namespace, deployment)
}

func assertReplicaSetsForbiddenUnderPreFixRules(t *testing.T, preFix kubernetes.Interface, namespace string) {
	t.Helper()
	_, err := preFix.AppsV1().ReplicaSets(namespace).List(context.Background(), metav1.ListOptions{})
	if err == nil {
		t.Fatal("listing ReplicaSets succeeded under the pre-#1083 rule set (no apps/replicasets grant) -- either the premise of #1083 is wrong or this cluster's RBAC is not enforcing the rule set this test built")
	}
	if !apierrors.IsForbidden(err) {
		t.Fatalf("listing ReplicaSets under the pre-#1083 rule set failed with %v, want a Forbidden error", err)
	}
	t.Logf("confirmed pre-#1083 rules: list replicasets => Forbidden (%v)", err)
}

// RoleBinding propagation is not instantaneous, so this polls a bounded window.
func assertReplicaSetsObservableUnderFixedRules(t *testing.T, fixed kubernetes.Interface, namespace string, deployment *appsv1.Deployment) {
	t.Helper()
	selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels).String()
	deadline := time.Now().Add(30 * time.Second)
	var list *appsv1.ReplicaSetList
	var err error
	for {
		list, err = fixed.AppsV1().ReplicaSets(namespace).List(context.Background(), metav1.ListOptions{LabelSelector: selector})
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Second)
	}
	mustNoErr(t, err, "list replicasets as the fixed provisioner identity")

	owned := ownedReplicaSet(t, list.Items, deployment.UID)
	if owned.Status.ReadyReplicas != 1 {
		t.Fatalf("owned ReplicaSet readyReplicas = %d, want 1 -- the fixed identity can list ReplicaSets, but this did not observe the ready signal a Deployment readiness wait actually reads", owned.Status.ReadyReplicas)
	}
	t.Logf("confirmed current rules: list replicasets => observed owned ReplicaSet with readyReplicas=%d", owned.Status.ReadyReplicas)
}

func renderProvisionerChart(t *testing.T, tenant string) []byte {
	t.Helper()
	chartDir, err := filepath.Abs(provisionerRBACChartDir)
	mustNoErr(t, err, "resolve chart dir")
	if _, statErr := os.Stat(chartDir); statErr != nil {
		t.Fatalf("locate chart at %s: %v", chartDir, statErr)
	}

	// #nosec G204 -- chartDir and tenant are test-controlled, not external input.
	cmd := exec.Command("helm", "template", "provisioner-rbac-probe", chartDir,
		"--set", "tenant="+tenant,
		"--set", "environment=probe",
		"--set-string", "api.envDeployer.enabled=true")
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("helm template %s: %v\n%s", chartDir, runErr, out)
	}
	return out
}

func renderProvisionerClusterRoleRules(t *testing.T, tenant string) []rbacv1.PolicyRule {
	t.Helper()
	roleName := tenant + "-env-provisioner"
	rules := decodeRulesByKind(t, renderProvisionerChart(t, tenant), "ClusterRole", roleName)
	if len(rules) == 0 {
		t.Fatalf("rendered chart carries no ClusterRole named %s with rules", roleName)
	}
	return rules
}

func renderProvisionerPlatformRoleRules(t *testing.T, tenant string) []rbacv1.PolicyRule {
	t.Helper()
	roleName := tenant + "-env-provisioner-platform"
	rules := decodeRulesByKind(t, renderProvisionerChart(t, tenant), "Role", roleName)
	if len(rules) == 0 {
		t.Fatalf("rendered chart carries no Role named %s with rules", roleName)
	}
	return rules
}

type renderedManifest struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Rules []rbacv1.PolicyRule `json:"rules,omitempty"`
}

func decodeRulesByKind(t *testing.T, rendered []byte, kind, roleName string) []rbacv1.PolicyRule {
	t.Helper()
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(rendered), 4096)
	for {
		var manifest renderedManifest
		if err := decoder.Decode(&manifest); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			t.Fatalf("decode rendered chart manifest: %v", err)
		}
		if manifest.Kind == kind && manifest.Metadata.Name == roleName {
			return manifest.Rules
		}
	}
}

var readinessRelevantResources = map[string]bool{
	"deployments":       true,
	"deployments/scale": true,
	"replicasets":       true,
	"pods":              true,
	"events":            true,
}

// filterRulesByResource keeps the probe within grants its namespaced-admin
// identity can hand out without tripping RBAC escalation checks.
func filterRulesByResource(rules []rbacv1.PolicyRule, resources map[string]bool) []rbacv1.PolicyRule {
	relevant := make([]rbacv1.PolicyRule, 0, len(rules))
	for _, rule := range rules {
		for _, resource := range rule.Resources {
			if resources[resource] {
				relevant = append(relevant, rule)
				break
			}
		}
	}
	return relevant
}

func readinessRelevantRules(rules []rbacv1.PolicyRule) []rbacv1.PolicyRule {
	return filterRulesByResource(rules, readinessRelevantResources)
}

var ingressRelevantResources = map[string]bool{"ingresses": true}

func ingressRelevantRules(rules []rbacv1.PolicyRule) []rbacv1.PolicyRule {
	return filterRulesByResource(rules, ingressRelevantResources)
}

func withoutReplicasetsGrant(rules []rbacv1.PolicyRule) []rbacv1.PolicyRule {
	without := make([]rbacv1.PolicyRule, 0, len(rules))
	for _, rule := range rules {
		if containsString(rule.APIGroups, "apps") && containsString(rule.Resources, "replicasets") {
			continue
		}
		without = append(without, rule)
	}
	return without
}

func withoutIngressGrant(rules []rbacv1.PolicyRule) []rbacv1.PolicyRule {
	without := make([]rbacv1.PolicyRule, 0, len(rules))
	for _, rule := range rules {
		if containsString(rule.APIGroups, "networking.k8s.io") && containsString(rule.Resources, "ingresses") {
			continue
		}
		without = append(without, rule)
	}
	return without
}

func withoutPodsExecGrant(rules []rbacv1.PolicyRule) []rbacv1.PolicyRule {
	without := make([]rbacv1.PolicyRule, 0, len(rules))
	for _, rule := range rules {
		if containsString(rule.APIGroups, "") && containsString(rule.Resources, "pods/exec") {
			continue
		}
		without = append(without, rule)
	}
	return without
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func provisionerRBACConfig(t *testing.T) *rest.Config {
	t.Helper()
	if kubeconfig := os.Getenv("ERUN_E2E_PROVISIONER_RBAC_KUBECONFIG"); kubeconfig != "" {
		cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
		mustNoErr(t, err, "build kubeconfig from ERUN_E2E_PROVISIONER_RBAC_KUBECONFIG")
		return cfg
	}
	cfg, err := rest.InClusterConfig()
	mustNoErr(t, err, "build in-cluster config (set ERUN_E2E_PROVISIONER_RBAC_KUBECONFIG to run out-of-cluster)")
	return cfg
}

func provisionerRBACNamespace(t *testing.T) string {
	t.Helper()
	if namespace := os.Getenv("ERUN_E2E_PROVISIONER_RBAC_NAMESPACE"); namespace != "" {
		return namespace
	}
	data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err == nil && strings.TrimSpace(string(data)) != "" {
		return strings.TrimSpace(string(data))
	}
	t.Fatal("set ERUN_E2E_PROVISIONER_RBAC_NAMESPACE: could not read this pod's in-cluster namespace file")
	return ""
}

// provisionerRBACProbeImage reuses this pod's already-pulled image unless
// ERUN_E2E_PROVISIONER_RBAC_IMAGE overrides it.
func provisionerRBACProbeImage(t *testing.T, admin kubernetes.Interface, namespace string) string {
	t.Helper()
	if image := os.Getenv("ERUN_E2E_PROVISIONER_RBAC_IMAGE"); image != "" {
		return image
	}
	podName := os.Getenv("HOSTNAME")
	if podName == "" {
		t.Fatal("set ERUN_E2E_PROVISIONER_RBAC_IMAGE: HOSTNAME is unset, so this pod's own image cannot be reused")
	}
	pod, err := admin.CoreV1().Pods(namespace).Get(context.Background(), podName, metav1.GetOptions{})
	mustNoErr(t, err, "read this pod's own image to reuse for the probe Deployment")
	if len(pod.Spec.Containers) == 0 {
		t.Fatalf("pod %s/%s carries no containers", namespace, podName)
	}
	return pod.Spec.Containers[0].Image
}

func createProbeDeployment(t *testing.T, admin kubernetes.Interface, namespace, name, image string) *appsv1.Deployment {
	t.Helper()
	labelSet := map[string]string{"app": name}
	replicas := int32(1)
	spec := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labelSet},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labelSet},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:    "probe",
						Image:   image,
						Command: []string{"sleep", "3600"},
					}},
				},
			},
		},
	}
	created, err := admin.AppsV1().Deployments(namespace).Create(context.Background(), spec, metav1.CreateOptions{})
	mustNoErr(t, err, "create probe Deployment")
	t.Cleanup(func() {
		policy := metav1.DeletePropagationForeground
		_ = admin.AppsV1().Deployments(namespace).Delete(context.Background(), created.Name, metav1.DeleteOptions{PropagationPolicy: &policy})
	})
	return created
}

func waitForDeploymentReady(t *testing.T, admin kubernetes.Interface, namespace, name string) *appsv1.Deployment {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		dep, err := admin.AppsV1().Deployments(namespace).Get(context.Background(), name, metav1.GetOptions{})
		mustNoErr(t, err, "get probe Deployment")
		if dep.Status.ReadyReplicas == 1 {
			return dep
		}
		if time.Now().After(deadline) {
			t.Fatalf("probe Deployment %s/%s never became ready (status: %+v)", namespace, name, dep.Status)
		}
		time.Sleep(2 * time.Second)
	}
}

// ownedReplicaSet disambiguates when a rollout left an old ReplicaSet behind.
func ownedReplicaSet(t *testing.T, items []appsv1.ReplicaSet, deploymentUID types.UID) *appsv1.ReplicaSet {
	t.Helper()
	for i := range items {
		for _, owner := range items[i].OwnerReferences {
			if owner.UID == deploymentUID {
				return &items[i]
			}
		}
	}
	t.Fatalf("no ReplicaSet in the listed set is owned by Deployment UID %s", deploymentUID)
	return nil
}

func provisionerRBACIdentity(t *testing.T, admin kubernetes.Interface, adminConfig *rest.Config, namespace, name string, rules []rbacv1.PolicyRule) kubernetes.Interface {
	t.Helper()
	client, _ := provisionerRBACIdentityWithConfig(t, admin, adminConfig, namespace, name, rules)
	return client
}

// provisionerRBACIdentityWithConfig also returns the impersonated config for
// pods/exec upgrades; empty rules bind no Role at all.
func provisionerRBACIdentityWithConfig(t *testing.T, admin kubernetes.Interface, adminConfig *rest.Config, namespace, name string, rules []rbacv1.PolicyRule) (kubernetes.Interface, *rest.Config) {
	t.Helper()
	ctx := context.Background()

	sa, err := admin.CoreV1().ServiceAccounts(namespace).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}, metav1.CreateOptions{})
	mustNoErr(t, err, "create probe ServiceAccount "+name)
	t.Cleanup(func() {
		_ = admin.CoreV1().ServiceAccounts(namespace).Delete(context.Background(), sa.Name, metav1.DeleteOptions{})
	})

	if len(rules) > 0 {
		role, err := admin.RbacV1().Roles(namespace).Create(ctx, &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Rules:      rules,
		}, metav1.CreateOptions{})
		mustNoErr(t, err, "create probe Role "+name)
		t.Cleanup(func() {
			_ = admin.RbacV1().Roles(namespace).Delete(context.Background(), role.Name, metav1.DeleteOptions{})
		})

		binding, err := admin.RbacV1().RoleBindings(namespace).Create(ctx, &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: sa.Name, Namespace: namespace}},
			RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: role.Name},
		}, metav1.CreateOptions{})
		mustNoErr(t, err, "create probe RoleBinding "+name)
		t.Cleanup(func() {
			_ = admin.RbacV1().RoleBindings(namespace).Delete(context.Background(), binding.Name, metav1.DeleteOptions{})
		})
	}

	impersonated := rest.CopyConfig(adminConfig)
	impersonated.Impersonate = rest.ImpersonationConfig{
		UserName: fmt.Sprintf("system:serviceaccount:%s:%s", namespace, sa.Name),
	}
	client, err := kubernetes.NewForConfig(impersonated)
	mustNoErr(t, err, "build impersonated kube client for "+name)
	return client, impersonated
}

func TestProvisionerClusterRoleGrantsIngressWrite(t *testing.T) {
	if os.Getenv("ERUN_E2E_PROVISIONER_RBAC") != "1" {
		t.Skip("opt-in: set ERUN_E2E_PROVISIONER_RBAC=1 on a live cluster where this identity may create Roles/ServiceAccounts/RoleBindings and impersonate, to prove the provisioner ClusterRole's ingresses grant against a real RBAC engine")
	}
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm is required to render the chart under test")
	}

	tenant := fmt.Sprintf("rbacprobe%d", time.Now().UnixNano())
	fixedRules := ingressRelevantRules(renderProvisionerClusterRoleRules(t, tenant))
	preFixRules := withoutIngressGrant(fixedRules)

	adminConfig := provisionerRBACConfig(t)
	namespace := provisionerRBACNamespace(t)
	admin, err := kubernetes.NewForConfig(adminConfig)
	mustNoErr(t, err, "build admin kube client")

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	fixed := provisionerRBACIdentity(t, admin, adminConfig, namespace, "erun-1089-ingress-fixed-"+suffix, fixedRules)
	preFix := provisionerRBACIdentity(t, admin, adminConfig, namespace, "erun-1089-ingress-prefix-"+suffix, preFixRules)

	ingressName := "erun-1089-ingress-probe-" + suffix
	assertIngressCreateForbiddenUnderPreFixRules(t, preFix, namespace, ingressName)
	assertIngressWriteSucceedsUnderFixedRules(t, admin, fixed, namespace, ingressName)
}

func assertIngressCreateForbiddenUnderPreFixRules(t *testing.T, preFix kubernetes.Interface, namespace, name string) {
	t.Helper()
	_, err := preFix.NetworkingV1().Ingresses(namespace).Create(context.Background(), probeIngress(name, namespace), metav1.CreateOptions{})
	if err == nil {
		t.Fatal("creating an Ingress succeeded under the pre-fix rule set (no networking.k8s.io/ingresses grant) -- either the premise of this test is wrong or this cluster's RBAC is not enforcing the rule set this test built")
	}
	if !apierrors.IsForbidden(err) {
		t.Fatalf("creating an Ingress under the pre-fix rule set failed with %v, want a Forbidden error", err)
	}
	t.Logf("confirmed pre-fix rules: create ingress => Forbidden (%v)", err)
}

// Create then update: re-expose must be idempotent.
func assertIngressWriteSucceedsUnderFixedRules(t *testing.T, admin, fixed kubernetes.Interface, namespace, name string) {
	t.Helper()
	created, err := fixed.NetworkingV1().Ingresses(namespace).Create(context.Background(), probeIngress(name, namespace), metav1.CreateOptions{})
	mustNoErr(t, err, "create ingress as the fixed provisioner identity")
	t.Cleanup(func() {
		_ = admin.NetworkingV1().Ingresses(namespace).Delete(context.Background(), created.Name, metav1.DeleteOptions{})
	})

	created.Spec.Rules[0].Host = "updated." + created.Spec.Rules[0].Host
	_, err = fixed.NetworkingV1().Ingresses(namespace).Update(context.Background(), created, metav1.UpdateOptions{})
	mustNoErr(t, err, "update ingress as the fixed provisioner identity")
	t.Logf("confirmed current rules: create+update ingress => succeeded")
}

func probeIngress(name, namespace string) *networkingv1.Ingress {
	pathType := networkingv1.PathTypePrefix
	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{
				Host: name + ".example.test",
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path:     "/",
							PathType: &pathType,
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: "probe",
									Port: networkingv1.ServiceBackendPort{Number: 80},
								},
							},
						}},
					},
				},
			}},
		},
	}
}
