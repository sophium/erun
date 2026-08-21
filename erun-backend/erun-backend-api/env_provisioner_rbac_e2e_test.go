package backendapi

// This file proves #1083's fix against a real Kubernetes RBAC engine rather
// than pinning a rule list: #1081 added a rule-pinning test (see
// erun-devops/k8s/erun-backend-api-chart_test.sh) that passed while
// provisioning stayed completely broken, because a test that only checks
// "does the rendered YAML contain resource X" can never catch a grant nobody
// thought to add. helm's own readiness wait for a Deployment walks
// Deployment -> its current ReplicaSet -> that ReplicaSet's ready count, and
// that object graph is not knowable from erun's own call sites -- it has to
// be observed against a real apiserver's authorizer.
//
// This test renders the actual provisioner ClusterRole from the actual
// chart (never a hand-copied rule list, so it cannot drift from what ships),
// binds it to a throwaway ServiceAccount via a namespaced Role/RoleBinding,
// stands up a real Deployment, and impersonates that ServiceAccount to
// perform the exact call helm's readiness wait makes: list the Deployment's
// ReplicaSets and read the owned one's ready count. It asserts that call
// succeeds under the current (fixed) rule set and is Forbidden under the
// rule set with the apps/replicasets grant removed -- i.e. the exact
// pre-#1083 state -- reproducing the issue's own live reproduction
// (`kubectl get replicasets --as=<deployer-sa>` => Forbidden) against a real
// cluster instead of asserting it once by hand.
//
// What this does NOT cover: it does not invoke the real `helm` binary or
// `erun deploy`, so a change to helm's own internal readiness algorithm (a
// different object graph) would not be caught here -- only a regression in
// the ClusterRole's grants for the Deployment -> ReplicaSet walk this test
// encodes. It also needs a live cluster where this identity may create
// Roles/ServiceAccounts/RoleBindings and impersonate another ServiceAccount,
// which is true of any erun-devops runtime pod (namespaced admin, see
// erun-devops/AGENTS.md "Runtime SA RBAC is namespaced by default") but not
// of a bare `go test ./...` on a laptop, so it is opt-in and skipped by
// default.

import (
	"bytes"
	"context"
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

// provisionerRBACChartDir is the chart this test renders the ClusterRole
// from -- the real source of truth, not a hand-copied rule list.
const provisionerRBACChartDir = "../../erun-devops/k8s/erun-backend-api"

// TestProvisionerClusterRoleGrantsHelmReadinessRead is the happy-path gate
// #1083 asks for: a healthy Deployment's readiness must actually be
// observable under the provisioner ClusterRole's real, current rules, and
// must NOT have been observable under the pre-#1083 rules.
func TestProvisionerClusterRoleGrantsHelmReadinessRead(t *testing.T) {
	if os.Getenv("ERUN_E2E_PROVISIONER_RBAC") != "1" {
		t.Skip("opt-in: set ERUN_E2E_PROVISIONER_RBAC=1 on a live cluster where this identity may create Roles/ServiceAccounts/RoleBindings and impersonate, to prove the provisioner ClusterRole's replicasets grant against a real RBAC engine (#1083)")
	}
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm is required to render the chart under test")
	}

	tenant := fmt.Sprintf("rbacprobe%d", time.Now().UnixNano())
	// Narrowed to the Deployment readiness object graph this test actually
	// exercises: the full ClusterRole also carries namespace-lifecycle and
	// quota-management grants (namespaces, resourcequotas, limitranges,
	// binding the admin ClusterRole) that this probe identity -- itself only
	// namespaced admin -- cannot self-escalate into granting, and does not
	// need to in order to prove the readiness-read contract.
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

// assertReplicaSetsForbiddenUnderPreFixRules reproduces the issue's own live
// reproduction: `kubectl get replicasets --as=<deployer-sa>` => Forbidden.
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

// assertReplicaSetsObservableUnderFixedRules performs the exact call helm's
// readiness wait makes for a Deployment: list its ReplicaSets by selector and
// read the owned one's ready count. RBAC propagation in a freshly created
// RoleBinding is not always instantaneous, so this polls a bounded window on
// the observable condition (the List call itself succeeding) rather than
// sleeping a fixed guess.
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

// renderProvisionerClusterRoleRules renders the real chart and extracts the
// current <tenant>-env-provisioner ClusterRole's rules, so this test can
// never drift from what actually ships.
func renderProvisionerClusterRoleRules(t *testing.T, tenant string) []rbacv1.PolicyRule {
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

	roleName := tenant + "-env-provisioner"
	rules := decodeClusterRoleRules(t, out, roleName)
	if len(rules) == 0 {
		t.Fatalf("rendered chart carries no ClusterRole named %s with rules", roleName)
	}
	return rules
}

// renderedManifest is the minimal shape shared by every kind this decode
// loop cares about: enough to find the ClusterRole by name and read its
// rules, and nothing else.
type renderedManifest struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Rules []rbacv1.PolicyRule `json:"rules,omitempty"`
}

// decodeClusterRoleRules walks helm template's multi-document YAML output
// looking for the named ClusterRole.
func decodeClusterRoleRules(t *testing.T, rendered []byte, roleName string) []rbacv1.PolicyRule {
	t.Helper()
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(rendered), 4096)
	for {
		var manifest renderedManifest
		if err := decoder.Decode(&manifest); err != nil {
			if err == io.EOF {
				return nil
			}
			t.Fatalf("decode rendered chart manifest: %v", err)
		}
		if manifest.Kind == "ClusterRole" && manifest.Metadata.Name == roleName {
			return manifest.Rules
		}
	}
}

// readinessRelevantResources is the object graph a Deployment readiness wait
// actually reads: the Deployment itself, its ReplicaSets, and the pods/events
// used for failure diagnostics elsewhere in this same ClusterRole (#1080).
var readinessRelevantResources = map[string]bool{
	"deployments":       true,
	"deployments/scale": true,
	"replicasets":       true,
	"pods":              true,
	"events":            true,
}

// readinessRelevantRules filters the rendered ClusterRole down to the rules
// this test exercises, so the probe identity only ever needs a subset of
// permissions its own (namespaced-admin) identity already holds -- the full
// ClusterRole also carries namespace-lifecycle and quota-management grants
// that a namespaced-admin probe cannot self-grant without tripping
// Kubernetes' RBAC escalation check, and that are irrelevant to the
// Deployment -> ReplicaSet readiness walk under test here.
func readinessRelevantRules(rules []rbacv1.PolicyRule) []rbacv1.PolicyRule {
	relevant := make([]rbacv1.PolicyRule, 0, len(rules))
	for _, rule := range rules {
		for _, resource := range rule.Resources {
			if readinessRelevantResources[resource] {
				relevant = append(relevant, rule)
				break
			}
		}
	}
	return relevant
}

// withoutReplicasetsGrant reproduces the pre-#1083 rule set by removing
// exactly the rule this fix adds, rather than a second hand-typed copy of
// the old rules that could itself drift from what #1083 actually changed.
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

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// provisionerRBACConfig builds the admin client config this test runs as.
// Defaulting to in-cluster config matches how this test is meant to run: from
// inside an erun-devops runtime pod, which already carries namespaced admin.
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

// provisionerRBACNamespace is where the probe's throwaway objects land.
// Defaulting to this pod's own namespace keeps the test self-contained: no
// tenant namespace is touched.
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

// provisionerRBACProbeImage reuses an image already pulled onto the node --
// this pod's own image -- so the probe Deployment schedules without a new
// pull. ERUN_E2E_PROVISIONER_RBAC_IMAGE overrides it for a kubeconfig-driven
// run against a different cluster, where this pod's own HOSTNAME is not a
// pod in the target namespace.
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

// createProbeDeployment stands up a real, minimal Deployment -- a healthy
// release in miniature -- for the readiness check under test to observe.
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

// waitForDeploymentReady polls the admin client -- unaffected by anything
// under test -- until the probe Deployment is a genuinely healthy release.
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

// ownedReplicaSet picks the ReplicaSet a Deployment actually owns out of a
// listed set, the same disambiguation a real readiness wait needs once a
// rollout has left an old ReplicaSet behind.
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

// provisionerRBACIdentity creates a throwaway ServiceAccount bound to rules
// via a namespaced Role/RoleBinding, then returns a client impersonating it
// -- the same `--as=system:serviceaccount:<ns>:<name>` mechanism the issue's
// own live reproduction used.
func provisionerRBACIdentity(t *testing.T, admin kubernetes.Interface, adminConfig *rest.Config, namespace, name string, rules []rbacv1.PolicyRule) kubernetes.Interface {
	t.Helper()
	ctx := context.Background()

	sa, err := admin.CoreV1().ServiceAccounts(namespace).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}, metav1.CreateOptions{})
	mustNoErr(t, err, "create probe ServiceAccount "+name)
	t.Cleanup(func() {
		_ = admin.CoreV1().ServiceAccounts(namespace).Delete(context.Background(), sa.Name, metav1.DeleteOptions{})
	})

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

	impersonated := rest.CopyConfig(adminConfig)
	impersonated.Impersonate = rest.ImpersonationConfig{
		UserName: fmt.Sprintf("system:serviceaccount:%s:%s", namespace, sa.Name),
	}
	client, err := kubernetes.NewForConfig(impersonated)
	mustNoErr(t, err, "build impersonated kube client for "+name)
	return client
}
