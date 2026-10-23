package oidcbootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/zitadel"
)

const (
	testExternalDomain = "auth.example.test"
	testPAT            = "org-owner-pat"
	testProject        = "test-platform"
	testNamespace      = "test-ns"
	testConfigMap      = "test-zitadel-oidc-clients"
	testPATsSecret     = "test-zitadel-pats"
)

// fakeManagementAPI is an in-memory Zitadel Management API serving the call
// shapes internal/zitadel issues for the OIDC applications. It refuses any
// call that does not carry the external domain as Host and the org-owner PAT
// as bearer, because Zitadel itself 404s a call with the wrong Host.
type fakeManagementAPI struct {
	mu       sync.Mutex
	nextID   int
	projects []fakeProject
	apps     []*fakeOIDCApp
	puts     []string
	creates  []string
	failPut  bool
	failGet  bool
}

type fakeProject struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type fakeOIDCApp struct {
	ID     string
	Name   string
	Config map[string]any
}

func (f *fakeManagementAPI) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Host != testExternalDomain || r.Header.Get("Authorization") != "Bearer "+testPAT {
		http.Error(w, `{"code":5,"message":"Not Found"}`, http.StatusNotFound)
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	path := strings.TrimPrefix(r.URL.Path, "/management/v1/projects")
	segments := strings.Split(strings.Trim(path, "/"), "/")
	// The route key is the method plus the path with the project and app ids
	// blanked out, which is all that distinguishes the endpoints.
	key := r.Method + " " + path
	if len(segments) >= 3 {
		key = r.Method + " " + strings.Join(append([]string{"*", segments[1], "*"}, segments[3:]...), "/")
		if segments[2] == "_search" || segments[2] == "oidc" {
			key = r.Method + " */" + segments[1] + "/" + segments[2]
		}
	}
	routes := map[string]func(){
		"POST /_search":            func() { writeJSON(w, map[string]any{"result": f.projects}) },
		"POST ":                    func() { f.createProject(w, body) },
		"POST */apps/_search":      func() { f.searchApps(w) },
		"POST */apps/oidc":         func() { f.createApp(w, body) },
		"PUT */apps/*/oidc_config": func() { f.updateApp(w, segments[2], body) },
		"GET */apps/*":             func() { f.getApp(w, segments[2]) },
	}
	if serve, ok := routes[key]; ok {
		serve()
		return
	}
	http.Error(w, "unexpected call "+r.Method+" "+r.URL.Path, http.StatusNotFound)
}

func (f *fakeManagementAPI) createProject(w http.ResponseWriter, body map[string]any) {
	f.nextID++
	id := fmt.Sprintf("id%d", f.nextID)
	f.projects = append(f.projects, fakeProject{ID: id, Name: body["name"].(string)})
	writeJSON(w, map[string]any{"id": id})
}

func (f *fakeManagementAPI) searchApps(w http.ResponseWriter) {
	results := []map[string]string{}
	for _, app := range f.apps {
		results = append(results, map[string]string{"id": app.ID, "name": app.Name})
	}
	writeJSON(w, map[string]any{"result": results})
}

func (f *fakeManagementAPI) createApp(w http.ResponseWriter, body map[string]any) {
	f.nextID++
	id := fmt.Sprintf("id%d", f.nextID)
	name := body["name"].(string)
	delete(body, "name")
	body["clientId"] = "cid-" + id
	f.apps = append(f.apps, &fakeOIDCApp{ID: id, Name: name, Config: body})
	f.creates = append(f.creates, name)
	writeJSON(w, map[string]any{"clientId": "cid-" + id})
}

func (f *fakeManagementAPI) updateApp(w http.ResponseWriter, id string, body map[string]any) {
	if f.failPut {
		http.Error(w, `{"message":"simulated failure"}`, http.StatusInternalServerError)
		return
	}
	app := f.app(id)
	if app == nil {
		http.Error(w, "no app", http.StatusNotFound)
		return
	}
	// The real endpoint replaces the whole config; only the client id, which
	// is not part of the update, survives on its own.
	body["clientId"] = app.Config["clientId"]
	app.Config = body
	f.puts = append(f.puts, id)
	writeJSON(w, map[string]any{})
}

func (f *fakeManagementAPI) getApp(w http.ResponseWriter, id string) {
	app := f.app(id)
	if app == nil || f.failGet {
		http.Error(w, "no app", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{"app": map[string]any{"id": app.ID, "name": app.Name, "oidcConfig": app.Config}})
}

func (f *fakeManagementAPI) app(id string) *fakeOIDCApp {
	for _, app := range f.apps {
		if app.ID == id {
			return app
		}
	}
	return nil
}

func (f *fakeManagementAPI) appNamed(name string) *fakeOIDCApp {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, app := range f.apps {
		if app.Name == name {
			return app
		}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

// seedApp registers a pre-existing project and app, the shape a real platform
// carries after first init.
func (f *fakeManagementAPI) seedApp(name string, appType string, redirects []string, logout []string) {
	f.projects = append(f.projects, fakeProject{ID: "p1", Name: testProject})
	config := map[string]any{
		"clientId":        "cid-existing",
		"redirectUris":    redirects,
		"responseTypes":   []string{"OIDC_RESPONSE_TYPE_CODE"},
		"grantTypes":      []string{"OIDC_GRANT_TYPE_AUTHORIZATION_CODE"},
		"appType":         appType,
		"authMethodType":  "OIDC_AUTH_METHOD_TYPE_NONE",
		"accessTokenType": "OIDC_TOKEN_TYPE_JWT",
		"devMode":         false,
		"clockSkew":       "0s",
	}
	if logout != nil {
		config["postLogoutRedirectUris"] = logout
	}
	f.apps = append(f.apps, &fakeOIDCApp{ID: "a1", Name: name, Config: config})
}

type harness struct {
	api  *fakeManagementAPI
	kube *fake.Clientset
	dir  string
	logs *bytes.Buffer
	idp  *zitadel.Client
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	api := &fakeManagementAPI{}
	server := httptest.NewServer(http.HandlerFunc(api.serve))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, AdminPATFile), testPAT+"\n")
	writeFile(t, filepath.Join(dir, LoginPATFile), "login-client-pat\n")
	idp, err := zitadel.NewClientFromFile(zitadel.Config{BaseURL: server.URL, ExternalDomain: testExternalDomain, PATPath: filepath.Join(dir, AdminPATFile)})
	if err != nil {
		t.Fatal(err)
	}
	return &harness{api: api, kube: fake.NewClientset(), dir: dir, logs: &bytes.Buffer{}, idp: idp}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) reconcile(t *testing.T, console []string, mobile []string) error {
	t.Helper()
	h.logs.Reset()
	reconciler := NewReconciler(Config{
		ProjectName:         testProject,
		ConfigMapName:       testConfigMap,
		PATsSecretName:      testPATsSecret,
		Namespace:           testNamespace,
		BootstrapDir:        h.dir,
		ConsoleRedirectURIs: console,
		MobileRedirectURIs:  mobile,
	}, h.idp, h.kube, log.New(h.logs, "oidc-bootstrap: ", 0))
	return reconciler.Reconcile(context.Background())
}

func (h *harness) configMap(t *testing.T) map[string]string {
	t.Helper()
	cm, err := h.kube.CoreV1().ConfigMaps(testNamespace).Get(context.Background(), testConfigMap, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read published ConfigMap: %v", err)
	}
	return cm.Data
}

func stringList(t *testing.T, value any) []string {
	t.Helper()
	if value == nil {
		return nil
	}
	encoded, _ := json.Marshal(value)
	var out []string
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("decode %v: %v", value, err)
	}
	return out
}

func redirects(t *testing.T, h *harness, name string) []string {
	t.Helper()
	app := h.api.appNamed(name)
	if app == nil {
		return nil
	}
	return stringList(t, app.Config["redirectUris"])
}

func assertLog(t *testing.T, h *harness, substring string) {
	t.Helper()
	if !strings.Contains(h.logs.String(), substring) {
		t.Fatalf("log does not contain %q:\n%s", substring, h.logs.String())
	}
}

func TestFirstTickCreatesTheAppsAndPublishesTheirClientIDs(t *testing.T) {
	h := newHarness(t)
	if err := h.reconcile(t, []string{"https://console.example.test/"}, nil); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	if got := redirects(t, h, ConsoleAppName); !reflect.DeepEqual(got, []string{"https://console.example.test/"}) {
		t.Fatalf("console redirects = %v", got)
	}
	console := h.api.appNamed(ConsoleAppName)
	if console.Config["appType"] != "OIDC_APP_TYPE_USER_AGENT" || console.Config["accessTokenType"] != "OIDC_TOKEN_TYPE_JWT" {
		t.Fatalf("console app config = %v", console.Config)
	}
	cli := h.api.appNamed(CLIAppName)
	if cli == nil || !reflect.DeepEqual(stringList(t, cli.Config["grantTypes"]), []string{"OIDC_GRANT_TYPE_AUTHORIZATION_CODE", "OIDC_GRANT_TYPE_DEVICE_CODE"}) {
		t.Fatalf("erun-cli must be created with both the auth code and device code grants: %v", cli)
	}
	data := h.configMap(t)
	if data[ConsoleClientIDKey] == "" || data[CLIClientIDKey] == "" || data[MobileClientIDKey] != "" {
		t.Fatalf("published client ids = %v", data)
	}
	assertLog(t, h, "oidc-bootstrap: ready")
}

func TestAChangedRedirectURIConvergesTheExistingApp(t *testing.T) {
	h := newHarness(t)
	h.api.seedApp(ConsoleAppName, "OIDC_APP_TYPE_USER_AGENT", []string{"https://old.example.test/"}, []string{"https://old.example.test/"})
	if err := h.reconcile(t, []string{"https://new.example.test/"}, nil); err != nil {
		t.Fatalf("converging tick: %v", err)
	}
	app := h.api.appNamed(ConsoleAppName)
	if got := stringList(t, app.Config["redirectUris"]); !reflect.DeepEqual(got, []string{"https://new.example.test/"}) {
		t.Fatalf("redirectUris must converge to the configured value, got %v", got)
	}
	if got := stringList(t, app.Config["postLogoutRedirectUris"]); !reflect.DeepEqual(got, []string{"https://new.example.test/"}) {
		t.Fatalf("postLogoutRedirectUris must converge alongside, got %v", got)
	}
	assertLog(t, h, `converged erun-console redirect URIs from ["https://old.example.test/"] to ["https://new.example.test/"]`)
	if slices.Contains(h.api.creates, ConsoleAppName) {
		t.Fatal("convergence must update the existing app, never create a duplicate")
	}
	if h.configMap(t)[ConsoleClientIDKey] != "cid-existing" {
		t.Fatalf("the existing app's client id must be published: %v", h.configMap(t))
	}
}

func TestConvergenceCarriesOverEveryFieldItDoesNotManage(t *testing.T) {
	h := newHarness(t)
	h.api.seedApp(ConsoleAppName, "OIDC_APP_TYPE_USER_AGENT", []string{"https://old.example.test/"}, []string{"https://old.example.test/"})
	if err := h.reconcile(t, []string{"https://new.example.test/"}, nil); err != nil {
		t.Fatal(err)
	}
	config := h.api.appNamed(ConsoleAppName).Config
	for field, want := range map[string]any{"accessTokenType": "OIDC_TOKEN_TYPE_JWT", "appType": "OIDC_APP_TYPE_USER_AGENT", "clockSkew": "0s", "devMode": false} {
		if config[field] != want {
			t.Fatalf("%s must survive a redirect-only convergence: got %v", field, config[field])
		}
	}
}

func TestAnUnchangedTickWritesNothing(t *testing.T) {
	h := newHarness(t)
	h.api.seedApp(ConsoleAppName, "OIDC_APP_TYPE_USER_AGENT", []string{"https://console.example.test/"}, []string{"https://console.example.test/"})
	if err := h.reconcile(t, []string{"https://console.example.test/"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(h.api.puts) != 0 {
		t.Fatalf("a tick with nothing to change must not write to the app: %v", h.api.puts)
	}
	if strings.Contains(h.logs.String(), "converged erun-console") {
		t.Fatalf("an unchanged tick must not log a convergence:\n%s", h.logs.String())
	}
}

func TestASecondConsoleOriginIsAdditiveAndADroppedOneIsRemoved(t *testing.T) {
	h := newHarness(t)
	h.api.seedApp(ConsoleAppName, "OIDC_APP_TYPE_USER_AGENT", []string{"https://console.example.test/"}, []string{"https://console.example.test/"})
	both := []string{"https://console.example.test/", "https://console2.example.test/"}
	if err := h.reconcile(t, both, nil); err != nil {
		t.Fatal(err)
	}
	if got := redirects(t, h, ConsoleAppName); !reflect.DeepEqual(sorted(got), sorted(both)) {
		t.Fatalf("both origins must be registered, got %v", got)
	}
	if err := h.reconcile(t, []string{"https://console.example.test/"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := redirects(t, h, ConsoleAppName); !reflect.DeepEqual(got, []string{"https://console.example.test/"}) {
		t.Fatalf("a URI dropped from the configured list must be dropped from the app, got %v", got)
	}
}

func TestNeverConvergesToAnEmptyList(t *testing.T) {
	h := newHarness(t)
	h.api.seedApp(ConsoleAppName, "OIDC_APP_TYPE_USER_AGENT", []string{"https://console.example.test/"}, []string{"https://console.example.test/"})
	if err := h.reconcile(t, nil, nil); err != nil {
		t.Fatalf("a tick with no configured console redirect must still succeed: %v", err)
	}
	if got := redirects(t, h, ConsoleAppName); !reflect.DeepEqual(got, []string{"https://console.example.test/"}) {
		t.Fatalf("the app must keep its registration, got %v", got)
	}
	if len(h.api.puts) != 0 {
		t.Fatalf("refusing an empty list must not write: %v", h.api.puts)
	}
	assertLog(t, h, `erun-console has no configured redirect URI; leaving its registered ["https://console.example.test/"] untouched`)
}

func TestAFailedUpdateIsLoudAndLeavesTheAppAsItWas(t *testing.T) {
	h := newHarness(t)
	h.api.seedApp(ConsoleAppName, "OIDC_APP_TYPE_USER_AGENT", []string{"https://old.example.test/"}, []string{"https://old.example.test/"})
	h.api.failPut = true
	err := h.reconcile(t, []string{"https://new.example.test/"}, nil)
	if err == nil {
		t.Fatal("a tick whose convergence failed must report it")
	}
	if got := redirects(t, h, ConsoleAppName); !reflect.DeepEqual(got, []string{"https://old.example.test/"}) {
		t.Fatalf("a failed update must leave the app unchanged, got %v", got)
	}
	assertLog(t, h, "FAILED to converge erun-console")
	// The app itself resolved, so its client id is still published.
	if h.configMap(t)[ConsoleClientIDKey] != "cid-existing" {
		t.Fatalf("client ids = %v", h.configMap(t))
	}
}

func TestMobileAppIsOptional(t *testing.T) {
	h := newHarness(t)
	if err := h.reconcile(t, []string{"https://console.example.test/"}, nil); err != nil {
		t.Fatal(err)
	}
	if h.api.appNamed(MobileAppName) != nil {
		t.Fatal("no erun-mobile app may be created while no mobile redirect is configured")
	}
	assertLog(t, h, "no mobile redirect URI configured; skipping the erun-mobile app")
	assertLog(t, h, "oidc-bootstrap: ready")
}

func TestMobileAppIsCreatedNativeWithAuthCodeOnly(t *testing.T) {
	h := newHarness(t)
	if err := h.reconcile(t, []string{"https://console.example.test/"}, []string{"erun://callback"}); err != nil {
		t.Fatal(err)
	}
	app := h.api.appNamed(MobileAppName)
	if app == nil || !reflect.DeepEqual(stringList(t, app.Config["redirectUris"]), []string{"erun://callback"}) {
		t.Fatalf("erun-mobile = %v", app)
	}
	if app.Config["appType"] != "OIDC_APP_TYPE_NATIVE" || !reflect.DeepEqual(stringList(t, app.Config["grantTypes"]), []string{"OIDC_GRANT_TYPE_AUTHORIZATION_CODE"}) {
		t.Fatalf("erun-mobile must be a native app with the auth code grant only: %v", app.Config)
	}
	assertLog(t, h, "mobile=cid-")
}

func TestMobileAppConvergesAChangedRedirect(t *testing.T) {
	h := newHarness(t)
	h.api.seedApp(MobileAppName, "OIDC_APP_TYPE_NATIVE", []string{"erun://old-callback"}, nil)
	if err := h.reconcile(t, nil, []string{"erun://new-callback"}); err != nil {
		t.Fatal(err)
	}
	if got := redirects(t, h, MobileAppName); !reflect.DeepEqual(got, []string{"erun://new-callback"}) {
		t.Fatalf("erun-mobile redirects = %v", got)
	}
	if _, ok := h.api.appNamed(MobileAppName).Config["postLogoutRedirectUris"]; ok {
		t.Fatal("erun-mobile's post-logout redirects are not managed and must not be invented")
	}
	assertLog(t, h, `converged erun-mobile redirect URIs from ["erun://old-callback"] to ["erun://new-callback"]`)
}

// A resolution failure must leave the previously published client ids alone
// rather than overwrite them with empty strings.
func TestAnUnresolvedAppLeavesThePublishedConfigMapUntouched(t *testing.T) {
	h := newHarness(t)
	h.api.seedApp(ConsoleAppName, "OIDC_APP_TYPE_USER_AGENT", []string{"https://console.example.test/"}, []string{"https://console.example.test/"})
	good := map[string]string{ConsoleClientIDKey: "good-console", CLIClientIDKey: "good-cli", MobileClientIDKey: ""}
	if _, err := h.kube.CoreV1().ConfigMaps(testNamespace).Create(context.Background(), &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: testConfigMap, Namespace: testNamespace},
		Data:       good,
	}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	h.api.failGet = true
	if err := h.reconcile(t, []string{"https://console.example.test/"}, nil); err == nil {
		t.Fatal("an app that could not be read must make the tick incomplete")
	}
	if got := h.configMap(t); !reflect.DeepEqual(got, good) {
		t.Fatalf("the ConfigMap must be left as it was, got %v", got)
	}
	assertLog(t, h, "resolution incomplete")
}

func TestPATsArePersistedAndRestored(t *testing.T) {
	h := newHarness(t)
	if err := h.reconcile(t, []string{"https://console.example.test/"}, nil); err != nil {
		t.Fatal(err)
	}
	secret, err := h.kube.CoreV1().Secrets(testNamespace).Get(context.Background(), testPATsSecret, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("PATs Secret not persisted: %v", err)
	}
	if string(secret.Data[AdminPATFile]) != testPAT+"\n" || string(secret.Data[LoginPATFile]) != "login-client-pat\n" {
		t.Fatalf("persisted PATs = %v", secret.Data)
	}

	restored := t.TempDir()
	if err := RestorePATs(context.Background(), h.kube, testNamespace, testPATsSecret, restored, log.New(h.logs, "", 0)); err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string]string{AdminPATFile: testPAT + "\n", LoginPATFile: "login-client-pat\n"} {
		got, err := os.ReadFile(filepath.Join(restored, file))
		if err != nil || string(got) != want {
			t.Fatalf("restored %s = %q, %v", file, got, err)
		}
	}
}

func TestPATsAreNotPersistedBeforeTheLoginPATExists(t *testing.T) {
	h := newHarness(t)
	if err := os.Remove(filepath.Join(h.dir, LoginPATFile)); err != nil {
		t.Fatal(err)
	}
	if err := h.reconcile(t, []string{"https://console.example.test/"}, nil); err != nil {
		t.Fatalf("a missing login PAT must not fail the app reconcile: %v", err)
	}
	if _, err := h.kube.CoreV1().Secrets(testNamespace).Get(context.Background(), testPATsSecret, metav1.GetOptions{}); err == nil {
		t.Fatal("no PATs Secret may be written while the login-client PAT is missing")
	}
	assertLog(t, h, "PATs Secret not persisted this tick")
}

func TestRestoreIsANoOpBeforeFirstInit(t *testing.T) {
	dir := t.TempDir()
	logs := &bytes.Buffer{}
	if err := RestorePATs(context.Background(), fake.NewClientset(), testNamespace, testPATsSecret, dir, log.New(logs, "", 0)); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("restore with no Secret must write nothing, wrote %v", entries)
	}
	if !strings.Contains(logs.String(), "first-instance init will mint the PATs") {
		t.Fatalf("log = %q", logs.String())
	}
}

func TestWaitForFileReturnsOnceTheFileHasContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), AdminPATFile)
	writeFile(t, path, "")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- WaitForFile(ctx, path, 1) }()
	writeFile(t, path, "pat")
	if err := <-done; err != nil {
		t.Fatalf("wait: %v", err)
	}
	cancel()
	if err := WaitForFile(ctx, filepath.Join(t.TempDir(), "absent"), 1); err == nil {
		t.Fatal("a cancelled wait must return the context's error")
	}
}
