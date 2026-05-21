package eruncommon

import (
	"os"
	"path/filepath"
	"testing"
)

func writeKubeconfig(t *testing.T, body string) func() {
	t.Helper()
	tmp := t.TempDir()
	dir := filepath.Join(tmp, ".kube")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	prev := kubeconfigUserHomeDir
	kubeconfigUserHomeDir = func() (string, error) { return tmp, nil }
	prevEnv, had := os.LookupEnv("KUBECONFIG")
	_ = os.Unsetenv("KUBECONFIG")
	return func() {
		kubeconfigUserHomeDir = prev
		if had {
			_ = os.Setenv("KUBECONFIG", prevEnv)
		} else {
			_ = os.Unsetenv("KUBECONFIG")
		}
	}
}

// TestLookupKubeContextBearerTokenFindsErunContext is the happy
// path — kubeconfig has a static-token user entry under the context
// name erun-001-020362606330-eu-west-2, and the lookup returns it.
func TestLookupKubeContextBearerTokenFindsErunContext(t *testing.T) {
	restore := writeKubeconfig(t, `apiVersion: v1
clusters: []
contexts: []
users:
- name: erun-001-020362606330-eu-west-2
  user:
    token: pbtrwcoJhOi5F3eWBkibV_tzr7AxIKr9ZCYWYjRwtbg
- name: arn:aws:eks:eu-west-1:123:cluster/foo
  user:
    exec:
      command: aws
`)
	defer restore()
	token, ok, err := LookupKubeContextBearerToken("erun-001-020362606330-eu-west-2")
	if err != nil || !ok {
		t.Fatalf("lookup: ok=%v err=%v", ok, err)
	}
	if token != "pbtrwcoJhOi5F3eWBkibV_tzr7AxIKr9ZCYWYjRwtbg" {
		t.Fatalf("token: %q", token)
	}
}

// TestLookupKubeContextBearerTokenMissingUserReturnsNotFound
// documents the contract for a doctor that needs to fall back to a
// different recovery surface (manual paste, SSM, ...) when the user
// entry is absent.
func TestLookupKubeContextBearerTokenMissingUserReturnsNotFound(t *testing.T) {
	restore := writeKubeconfig(t, `users: []`)
	defer restore()
	_, ok, err := LookupKubeContextBearerToken("missing")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Fatal("expected miss")
	}
}

// TestLookupKubeContextBearerTokenExecOnlyUserReturnsNotFound covers
// the case where a user entry exists but uses exec auth instead of
// a static token. erun's restore flow needs the literal bearer
// token; an exec entry isn't usable here.
func TestLookupKubeContextBearerTokenExecOnlyUserReturnsNotFound(t *testing.T) {
	restore := writeKubeconfig(t, `users:
- name: my-ctx
  user:
    exec:
      command: aws
`)
	defer restore()
	_, ok, err := LookupKubeContextBearerToken("my-ctx")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Fatal("exec-only user should not yield a token")
	}
}

// TestLookupKubeContextBearerTokenHonorsKUBECONFIG verifies the env
// override takes precedence and that a multi-path KUBECONFIG list
// honors the documented "first entry wins" rule.
func TestLookupKubeContextBearerTokenHonorsKUBECONFIG(t *testing.T) {
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	if err := os.WriteFile(first, []byte(`users:
- name: ctx
  user:
    token: from-first
`), 0o600); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := os.WriteFile(second, []byte(`users:
- name: ctx
  user:
    token: from-second
`), 0o600); err != nil {
		t.Fatalf("second: %v", err)
	}
	prev, had := os.LookupEnv("KUBECONFIG")
	_ = os.Setenv("KUBECONFIG", first+string(os.PathListSeparator)+second)
	defer func() {
		if had {
			_ = os.Setenv("KUBECONFIG", prev)
		} else {
			_ = os.Unsetenv("KUBECONFIG")
		}
	}()
	token, ok, err := LookupKubeContextBearerToken("ctx")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if token != "from-first" {
		t.Fatalf("token: %q (expected from-first)", token)
	}
}

// TestLookupKubeContextBearerTokenMissingFile: a fresh host with no
// kubeconfig at all must produce ok=false rather than a hard error.
func TestLookupKubeContextBearerTokenMissingFile(t *testing.T) {
	tmp := t.TempDir()
	prev := kubeconfigUserHomeDir
	kubeconfigUserHomeDir = func() (string, error) { return tmp, nil }
	prevEnv, had := os.LookupEnv("KUBECONFIG")
	_ = os.Unsetenv("KUBECONFIG")
	defer func() {
		kubeconfigUserHomeDir = prev
		if had {
			_ = os.Setenv("KUBECONFIG", prevEnv)
		} else {
			_ = os.Unsetenv("KUBECONFIG")
		}
	}()
	_, ok, err := LookupKubeContextBearerToken("anything")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Fatal("expected miss when ~/.kube/config does not exist")
	}
}
