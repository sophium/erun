package deployexec

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestBootstrapEnvironmentScriptDefaultsToInCluster(t *testing.T) {
	script := bootstrapEnvironmentScript("acme", "prod", PlacementParams{})
	if !strings.Contains(script, "current-context: in-cluster") {
		t.Fatalf("script = %q, want the in-cluster kubeconfig", script)
	}
	if !strings.Contains(script, "kubernetescontext: in-cluster") {
		t.Fatalf("script = %q, want the seeded env config to name in-cluster", script)
	}
	if strings.Contains(script, "kubectl config") {
		t.Fatalf("script = %q, an in-cluster placement must not shell out to kubectl", script)
	}
}

// TestBootstrapEnvironmentScriptTargetsRemoteContext locks the #1112 kubeconfig
// shape for a placed environment: kubectl config commands (never a hand-rolled
// YAML heredoc interpolating the context name/server URL, which an operator-
// authored context name could otherwise inject into) and the admin token read
// back from an env var, never a literal value.
func TestBootstrapEnvironmentScriptTargetsRemoteContext(t *testing.T) {
	placement := PlacementParams{
		ContextID:         "ctx-1",
		KubernetesContext: "prod-cluster",
		ServerURL:         "https://203.0.113.10:6443",
	}
	script := bootstrapEnvironmentScript("acme", "prod", placement)
	for _, want := range []string{
		"'kubectl' 'config' 'set-cluster' 'prod-cluster' '--server' 'https://203.0.113.10:6443' '--insecure-skip-tls-verify=true'",
		`'kubectl' 'config' 'set-credentials' 'prod-cluster' '--token' "$ERUN_PLACEMENT_ADMIN_TOKEN"`,
		"'kubectl' 'config' 'set-context' 'prod-cluster' '--cluster' 'prod-cluster' '--user' 'prod-cluster'",
		"'kubectl' 'config' 'use-context' 'prod-cluster'",
		"kubernetescontext: prod-cluster",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script = %q, want it to contain %q", script, want)
		}
	}
	if strings.Contains(script, "in-cluster") {
		t.Fatalf("script = %q, a remote placement must not seed the in-cluster kubeconfig too", script)
	}
}

// TestBootstrapEnvironmentScriptQuotesUntrustedContextName proves an
// operator-authored context name cannot break out of its argv position:
// shellJoin single-quotes it, so a shell metacharacter lands inside the
// quotes as literal text passed to kubectl, never interpreted by the shell.
func TestBootstrapEnvironmentScriptQuotesUntrustedContextName(t *testing.T) {
	placement := PlacementParams{
		ContextID:         "ctx-1",
		KubernetesContext: "prod'; rm -rf /;'",
		ServerURL:         "https://203.0.113.10:6443",
	}
	script := bootstrapEnvironmentScript("acme", "prod", placement)
	if !strings.Contains(script, `'kubectl' 'config' 'set-cluster' 'prod'\''; rm -rf /;'\''' '--server'`) {
		t.Fatalf("script = %q, want the malicious name single-quote-escaped as one argv element", script)
	}
}

func TestPlacementEnvVarsEmptyForInClusterPlacement(t *testing.T) {
	if vars := placementEnvVars(PlacementParams{}); vars != nil {
		t.Fatalf("placementEnvVars(zero) = %v, want nil", vars)
	}
}

func TestPlacementEnvVarsSourceTheSecretForRemotePlacement(t *testing.T) {
	vars := placementEnvVars(PlacementParams{ContextID: "ctx-1", KubernetesContext: "prod-cluster", ServerURL: "https://203.0.113.10:6443"})
	if len(vars) != 1 {
		t.Fatalf("placementEnvVars = %v, want exactly one entry", vars)
	}
	v := vars[0]
	if v.Name != placementAdminTokenEnvVar {
		t.Fatalf("env var name = %q, want %q", v.Name, placementAdminTokenEnvVar)
	}
	if v.ValueFrom == nil || v.ValueFrom.SecretKeyRef == nil {
		t.Fatalf("env var = %+v, want a SecretKeyRef source (never a literal Value)", v)
	}
	if v.Value != "" {
		t.Fatalf("env var Value = %q, want empty — the token must never sit in the Job spec directly", v.Value)
	}
	if v.ValueFrom.SecretKeyRef.Name != PlacementSecretName("ctx-1") {
		t.Fatalf("secret ref name = %q, want %q", v.ValueFrom.SecretKeyRef.Name, PlacementSecretName("ctx-1"))
	}
	if v.ValueFrom.SecretKeyRef.Key != placementAdminTokenKey {
		t.Fatalf("secret ref key = %q, want %q", v.ValueFrom.SecretKeyRef.Key, placementAdminTokenKey)
	}
}

func TestEnsurePlacementSecretNoopForInClusterPlacement(t *testing.T) {
	kube := fake.NewSimpleClientset()
	if err := ensurePlacementSecret(context.Background(), kube, "acme-platform", PlacementParams{}); err != nil {
		t.Fatalf("ensurePlacementSecret: %v", err)
	}
	secrets, err := kube.CoreV1().Secrets("acme-platform").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list secrets: %v", err)
	}
	if len(secrets.Items) != 0 {
		t.Fatalf("secrets = %v, want none created for an in-cluster placement", secrets.Items)
	}
}

// TestEnsurePlacementSecretCreatesThenUpdates locks the upsert: the first
// call creates the Secret, and a second call with a rotated token updates the
// same object in place rather than erroring on AlreadyExists — a repeat
// deploy or a token rotation must both reach the very next Job. Asserted via
// StringData: the real API server converts it to Data on write, but the fake
// clientset used here stores the object as given, without that conversion.
func TestEnsurePlacementSecretCreatesThenUpdates(t *testing.T) {
	kube := fake.NewSimpleClientset()
	placement := PlacementParams{ContextID: "ctx-1", AdminToken: "token-v1"}
	if err := ensurePlacementSecret(context.Background(), kube, "acme-platform", placement); err != nil {
		t.Fatalf("ensurePlacementSecret (create): %v", err)
	}
	secret, err := kube.CoreV1().Secrets("acme-platform").Get(context.Background(), PlacementSecretName("ctx-1"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if secret.StringData[placementAdminTokenKey] != "token-v1" {
		t.Fatalf("secret token = %q, want token-v1", secret.StringData[placementAdminTokenKey])
	}

	placement.AdminToken = "token-v2"
	if err := ensurePlacementSecret(context.Background(), kube, "acme-platform", placement); err != nil {
		t.Fatalf("ensurePlacementSecret (update): %v", err)
	}
	secret, err = kube.CoreV1().Secrets("acme-platform").Get(context.Background(), PlacementSecretName("ctx-1"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret after update: %v", err)
	}
	if secret.StringData[placementAdminTokenKey] != "token-v2" {
		t.Fatalf("secret token after rotation = %q, want token-v2", secret.StringData[placementAdminTokenKey])
	}
}

func TestResolvePlacementTokenEmptyContextIsNoop(t *testing.T) {
	token, err := ResolvePlacementToken(context.Background(), nil, "")
	if err != nil || token != "" {
		t.Fatalf("ResolvePlacementToken(empty) = (%q, %v), want (\"\", nil)", token, err)
	}
}

func TestResolvePlacementTokenFailsClearlyWithNoResolver(t *testing.T) {
	_, err := ResolvePlacementToken(context.Background(), nil, "ctx-1")
	if err == nil {
		t.Fatal("expected an error when a context is named but no resolver is configured")
	}
}

type stubCredentialResolver struct {
	token string
	err   error
}

func (s stubCredentialResolver) Get(context.Context, string) (string, error) {
	return s.token, s.err
}

func TestResolvePlacementTokenUsesTheResolver(t *testing.T) {
	token, err := ResolvePlacementToken(context.Background(), stubCredentialResolver{token: "secret-token"}, "ctx-1")
	if err != nil {
		t.Fatalf("ResolvePlacementToken: %v", err)
	}
	if token != "secret-token" {
		t.Fatalf("token = %q, want secret-token", token)
	}
}
