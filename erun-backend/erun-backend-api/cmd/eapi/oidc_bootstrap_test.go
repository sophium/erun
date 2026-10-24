package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/oidcbootstrap"
)

// The variable names are a contract with the erun-zitadel chart.
func TestOIDCBootstrapConfigReadsTheChartEnvironment(t *testing.T) {
	t.Setenv("CORE_PORT", "9090")
	t.Setenv("EXTERNAL_DOMAIN", "auth.example.test")
	t.Setenv("PROJECT_NAME", "erun-platform")
	t.Setenv("CONFIGMAP_NAME", "erun-zitadel-oidc-clients")
	t.Setenv("PATS_SECRET_NAME", "erun-zitadel-pats")
	t.Setenv("POD_NAMESPACE", "erun")
	t.Setenv("BOOTSTRAP_DIR", "/zitadel/bootstrap")
	t.Setenv("CONSOLE_REDIRECT_URIS", `["https://console.example.test/"]`)
	t.Setenv("MOBILE_REDIRECT_URIS", `["erun://callback"]`)
	t.Setenv("OIDC_BOOTSTRAP_RUN_ONCE", "1")

	cfg, err := resolveOIDCBootstrapConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CorePort != 9090 || cfg.ExternalDomain != "auth.example.test" || !cfg.Once || cfg.RestorePATs {
		t.Fatalf("config = %+v", cfg)
	}
	rc, err := cfg.reconcilerConfig()
	if err != nil {
		t.Fatal(err)
	}
	want := oidcbootstrap.Config{
		ProjectName: "erun-platform", ConfigMapName: "erun-zitadel-oidc-clients", PATsSecretName: "erun-zitadel-pats",
		Namespace: "erun", BootstrapDir: "/zitadel/bootstrap",
		ConsoleRedirectURIs: []string{"https://console.example.test/"}, MobileRedirectURIs: []string{"erun://callback"},
	}
	if !reflect.DeepEqual(rc, want) {
		t.Fatalf("reconciler config = %+v, want %+v", rc, want)
	}
}

func TestOIDCBootstrapConfigNamesWhatIsMissing(t *testing.T) {
	for _, name := range []string{"EXTERNAL_DOMAIN", "PROJECT_NAME", "CONFIGMAP_NAME", "PATS_SECRET_NAME", "POD_NAMESPACE"} {
		t.Setenv(name, "")
	}
	_, err := resolveOIDCBootstrapConfig(nil)
	if err == nil || !strings.Contains(err.Error(), "--external-domain (EXTERNAL_DOMAIN)") || !strings.Contains(err.Error(), "--namespace (POD_NAMESPACE)") {
		t.Fatalf("err = %v", err)
	}
	_, err = resolveOIDCBootstrapConfig([]string{"--restore-pats", "--namespace", "erun", "--pats-secret-name", "erun-zitadel-pats"})
	if err != nil {
		t.Fatalf("restore-pats config: %v", err)
	}
}

func TestOIDCBootstrapRejectsAMalformedRedirectList(t *testing.T) {
	cfg := oidcBootstrapConfig{ConsoleRedirectURIs: "https://not-json/"}
	if _, err := cfg.reconcilerConfig(); err == nil || !strings.Contains(err.Error(), "JSON array") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunDispatchesTheOIDCBootstrapSubcommand(t *testing.T) {
	err := run([]string{oidcBootstrapCommand, "unexpected"})
	if err == nil || !strings.Contains(err.Error(), "takes no arguments") {
		t.Fatalf("run must hand oidc-bootstrap its own arguments, got %v", err)
	}
}

func TestOIDCBootstrapOnceReconcilesAgainstTheLoopbackCore(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "admin-sa.pat"), []byte("pat\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	core := &fakeCore{}
	server := httptest.NewServer(core)
	defer server.Close()

	kube := fake.NewClientset()
	cfg := oidcBootstrapConfig{
		CorePort: serverPort(t, server), ExternalDomain: "auth.example.test", ProjectName: "erun-platform",
		ConfigMapName: "clients", PATsSecretName: "pats", Namespace: "erun", BootstrapDir: dir,
		ConsoleRedirectURIs: `["https://console.example.test/"]`, MobileRedirectURIs: "[]", Once: true,
	}
	logs := &bytes.Buffer{}
	if err := runOIDCBootstrapWith(context.Background(), cfg, kube, log.New(logs, "", 0)); err != nil {
		t.Fatalf("once: %v\n%s", err, logs.String())
	}
	if len(core.badCalls) > 0 {
		t.Fatalf("Management API calls without the external domain as Host or the PAT as bearer: %v", core.badCalls)
	}
	cm, err := kube.CoreV1().ConfigMaps("erun").Get(context.Background(), "clients", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cm.Data["consoleClientId"] == "" || cm.Data["cliClientId"] == "" || cm.Data["mobileClientId"] != "" {
		t.Fatalf("published = %v", cm.Data)
	}
}

// fakeCore is an empty Management API where every create succeeds.
type fakeCore struct {
	mu       sync.Mutex
	nextID   int
	badCalls []string
}

func (c *fakeCore) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r.Host != "auth.example.test" || r.Header.Get("Authorization") != "Bearer pat" {
		c.badCalls = append(c.badCalls, r.Method+" "+r.URL.Path)
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.HasSuffix(r.URL.Path, "/_search"):
		_, _ = w.Write([]byte(`{"result":[]}`))
	case r.URL.Path == "/management/v1/projects":
		_, _ = w.Write([]byte(`{"id":"p1"}`))
	default:
		c.nextID++
		if err := json.NewEncoder(w).Encode(map[string]string{"clientId": "cid-" + strconv.Itoa(c.nextID)}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

func serverPort(t *testing.T, server *httptest.Server) int {
	t.Helper()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestOIDCBootstrapRestorePATsSeedsTheBootstrapDirectory(t *testing.T) {
	dir := t.TempDir()
	kube := fake.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "pats", Namespace: "erun"},
		Data:       map[string][]byte{"admin-sa.pat": []byte("admin\n"), "login-client.pat": []byte("login\n")},
	})
	cfg := oidcBootstrapConfig{PATsSecretName: "pats", Namespace: "erun", BootstrapDir: dir, RestorePATs: true}
	if err := runOIDCBootstrapWith(context.Background(), cfg, kube, log.New(&bytes.Buffer{}, "", 0)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "login-client.pat"))
	if err != nil || string(got) != "login\n" {
		t.Fatalf("restored login PAT = %q, %v", got, err)
	}
}
