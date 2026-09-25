package zitadel

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// fakeZitadel is a stand-in Management API holding just enough state to
// distinguish "the identity is not there" from "it already is": the project
// list and the applications created in it. It records every call so a test can
// assert what provisioning did *not* do on a second pass, which is the half
// idempotence actually rests on.
type fakeZitadel struct {
	mu sync.Mutex
	// projects is every project the org holds, by name.
	projects map[string]string
	// apps is every application, by (projectID, name).
	apps map[string]fakeApp
	// calls records "METHOD path" for every request served.
	calls []string
}

type fakeApp struct {
	id             string
	accessTokenTyp string
	clientID       string
	clientSecret   string
}

func newFakeZitadel() *fakeZitadel {
	return &fakeZitadel{projects: map[string]string{}, apps: map[string]fakeApp{}}
}

func (f *fakeZitadel) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// countExact counts calls whose "METHOD path" is exactly call. Exact rather
// than prefix-matched because the collection endpoints are prefixes of their
// own create endpoints: a search for "POST /management/v1/projects" would
// otherwise also count the "_search" beside it, and the assertion that a
// second pass creates nothing would pass for the wrong reason.
func (f *fakeZitadel) countExact(call string) int {
	count := 0
	for _, recorded := range f.recorded() {
		if recorded == call {
			count++
		}
	}
	return count
}

func (f *fakeZitadel) countContains(substring string) int {
	count := 0
	for _, recorded := range f.recorded() {
		if strings.Contains(recorded, substring) {
			count++
		}
	}
	return count
}

func (f *fakeZitadel) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		for _, route := range f.routes() {
			if route.matches(r) {
				route.serve(w, r)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}
}

// fakeRoute is one endpoint of the stand-in API. Keyed off a matcher rather
// than a switch so the handler stays a loop: the matchers are the whole of the
// routing, and each is small enough to read at a glance.
type fakeRoute struct {
	matches func(*http.Request) bool
	serve   func(http.ResponseWriter, *http.Request)
}

func (f *fakeZitadel) routes() []fakeRoute {
	return []fakeRoute{
		{postTo("/management/v1/projects/_search"), func(w http.ResponseWriter, _ *http.Request) { f.searchProjects(w) }},
		{postTo("/management/v1/projects"), f.createProject},
		{postSuffixed("/apps/_search"), f.searchApps},
		{postSuffixed("/apps/api"), f.createAPIApp},
		{getContaining("/apps/"), f.getApp},
		{putSuffixed("/api_config"), f.updateAPIConfig},
		{deleteContaining("/apps/"), f.deleteApp},
	}
}

func postTo(path string) func(*http.Request) bool {
	return func(r *http.Request) bool { return r.Method == http.MethodPost && r.URL.Path == path }
}

func postSuffixed(suffix string) func(*http.Request) bool {
	return func(r *http.Request) bool {
		return r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, suffix)
	}
}

func putSuffixed(suffix string) func(*http.Request) bool {
	return func(r *http.Request) bool { return r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, suffix) }
}

func deleteContaining(substring string) func(*http.Request) bool {
	return func(r *http.Request) bool {
		return r.Method == http.MethodDelete && strings.Contains(r.URL.Path, substring)
	}
}

func getContaining(substring string) func(*http.Request) bool {
	return func(r *http.Request) bool {
		return r.Method == http.MethodGet && strings.Contains(r.URL.Path, substring)
	}
}

func (f *fakeZitadel) searchProjects(w http.ResponseWriter) {
	result := make([]map[string]string, 0, len(f.projects))
	for name, id := range f.projects {
		result = append(result, map[string]string{"id": id, "name": name})
	}
	writeFakeJSON(w, map[string]any{"result": result})
}

func (f *fakeZitadel) createProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	id := "project-" + body.Name
	f.projects[body.Name] = id
	writeFakeJSON(w, map[string]any{"id": id})
}

func (f *fakeZitadel) searchApps(w http.ResponseWriter, r *http.Request) {
	projectID := projectIDFromPath(r.URL.Path)
	result := make([]map[string]string, 0)
	for key, app := range f.apps {
		if strings.HasPrefix(key, projectID+"\x00") {
			result = append(result, map[string]string{"id": app.id, "name": strings.TrimPrefix(key, projectID+"\x00")})
		}
	}
	writeFakeJSON(w, map[string]any{"result": result})
}

func (f *fakeZitadel) createAPIApp(w http.ResponseWriter, r *http.Request) {
	projectID := projectIDFromPath(r.URL.Path)
	var body struct {
		Name            string `json:"name"`
		AccessTokenType string `json:"accessTokenType"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	app := fakeApp{
		id:             "app-" + body.Name,
		accessTokenTyp: body.AccessTokenType,
		clientID:       "client-" + body.Name,
		clientSecret:   "secret-" + body.Name,
	}
	f.apps[projectID+"\x00"+body.Name] = app
	writeFakeJSON(w, map[string]any{"appId": app.id, "clientId": app.clientID, "clientSecret": app.clientSecret})
}

func (f *fakeZitadel) getApp(w http.ResponseWriter, r *http.Request) {
	projectID := projectIDFromPath(r.URL.Path)
	appID := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	for key, app := range f.apps {
		if strings.HasPrefix(key, projectID+"\x00") && app.id == appID {
			writeFakeJSON(w, map[string]any{"app": map[string]any{
				"id":   app.id,
				"name": strings.TrimPrefix(key, projectID+"\x00"),
				"apiConfig": map[string]any{
					"clientId":        app.clientID,
					"clientSecret":    app.clientSecret,
					"accessTokenType": app.accessTokenTyp,
				},
			}})
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
}

func (f *fakeZitadel) updateAPIConfig(w http.ResponseWriter, r *http.Request) {
	projectID := projectIDFromPath(r.URL.Path)
	trimmed := strings.TrimSuffix(r.URL.Path, "/api_config")
	appID := trimmed[strings.LastIndex(trimmed, "/")+1:]
	for key, app := range f.apps {
		if !strings.HasPrefix(key, projectID+"\x00") || app.id != appID {
			continue
		}
		var body struct {
			AccessTokenType string `json:"accessTokenType"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		app.accessTokenTyp = body.AccessTokenType
		f.apps[key] = app
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (f *fakeZitadel) deleteApp(w http.ResponseWriter, r *http.Request) {
	projectID := projectIDFromPath(r.URL.Path)
	appID := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	for key, app := range f.apps {
		if strings.HasPrefix(key, projectID+"\x00") && app.id == appID {
			delete(f.apps, key)
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
}

// holdsApp reports whether an application of this name is still in the
// project — the credential half of what a revocation has to leave behind.
func (f *fakeZitadel) holdsApp(projectID string, name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.apps[projectID+"\x00"+name]
	return ok
}

func writeFakeJSON(w http.ResponseWriter, body any) {
	_ = json.NewEncoder(w).Encode(body)
}

func projectIDFromPath(path string) string {
	rest := strings.TrimPrefix(path, "/management/v1/projects/")
	return rest[:strings.Index(rest, "/")]
}

// TestEnsureMachineIdentityCreatesTheIdentityAndReturnsItsCredential is the
// ordinary first call: no project and no application exist yet, so both are
// created and the client-credentials pair comes back.
func TestEnsureMachineIdentityCreatesTheIdentityAndReturnsItsCredential(t *testing.T) {
	fake := newFakeZitadel()
	client, _ := newTestClient(t, fake.handler())

	identity, err := client.EnsureMachineIdentity(context.Background(), EnsureMachineIdentityParams{
		OrgID:     "org-1",
		LoginName: "erun-env-0192",
	})
	if err != nil {
		t.Fatalf("EnsureMachineIdentity: %v", err)
	}
	if identity.ClientID != "client-erun-env-0192" || identity.ClientSecret != "secret-erun-env-0192" {
		t.Fatalf("unexpected credentials %+v", identity)
	}
	if creates := fake.countExact("POST /management/v1/projects"); creates != 1 {
		t.Fatalf("expected exactly one project create, got %d, calls %v", creates, fake.recorded())
	}
	if creates := fake.countContains("/apps/api"); creates != 1 {
		t.Fatalf("expected exactly one application create, got %d, calls %v", creates, fake.recorded())
	}
}

// TestEnsureMachineIdentityIsIdempotentForOneLoginName is the property
// provisioning an environment twice rests on: the second call finds the
// application the first one created and creates nothing.
func TestEnsureMachineIdentityIsIdempotentForOneLoginName(t *testing.T) {
	fake := newFakeZitadel()
	client, _ := newTestClient(t, fake.handler())

	first, err := client.EnsureMachineIdentity(context.Background(), EnsureMachineIdentityParams{OrgID: "org-1", LoginName: "erun-env-0192"})
	if err != nil {
		t.Fatalf("first EnsureMachineIdentity: %v", err)
	}
	second, err := client.EnsureMachineIdentity(context.Background(), EnsureMachineIdentityParams{OrgID: "org-1", LoginName: "erun-env-0192"})
	if err != nil {
		t.Fatalf("second EnsureMachineIdentity: %v", err)
	}

	if second != first {
		t.Fatalf("second call returned %+v, want the identity the first one created (%+v)", second, first)
	}
	if created := fake.countContains("/apps/api"); created != 1 {
		t.Fatalf("expected the second call to create nothing, but the application-create endpoint was called %d times", created)
	}
	if projects := fake.countExact("POST /management/v1/projects"); projects != 1 {
		t.Fatalf("expected the second call to create no project, but the project-create endpoint was called %d times", projects)
	}
}

// TestEnsureMachineIdentityConvergesAnOpaqueAccessTokenType covers the one
// property that silently makes an identity useless: a token that is not a JWT
// cannot carry the org claim an org-scoped issuer resolves the tenant from, so
// an application found on the opaque default is corrected rather than returned
// as it stands.
func TestEnsureMachineIdentityConvergesAnOpaqueAccessTokenType(t *testing.T) {
	fake := newFakeZitadel()
	fake.projects[machineIdentityProjectName] = "project-1"
	fake.apps["project-1\x00erun-env-0192"] = fakeApp{
		id:             "app-erun-env-0192",
		accessTokenTyp: "API_TOKEN_TYPE_BEARER",
		clientID:       "client-erun-env-0192",
		clientSecret:   "secret-erun-env-0192",
	}
	client, _ := newTestClient(t, fake.handler())

	identity, err := client.EnsureMachineIdentity(context.Background(), EnsureMachineIdentityParams{OrgID: "org-1", LoginName: "erun-env-0192"})
	if err != nil {
		t.Fatalf("EnsureMachineIdentity: %v", err)
	}
	if identity.ClientSecret != "secret-erun-env-0192" {
		t.Fatalf("unexpected credential %+v", identity)
	}
	if got := fake.apps["project-1\x00erun-env-0192"].accessTokenTyp; got != apiTokenTypeJWT {
		t.Fatalf("access token type converged to %q, want %q", got, apiTokenTypeJWT)
	}
	if corrections := fake.countContains("PUT /management/v1/projects"); corrections != 1 {
		t.Fatalf("expected exactly one config correction, got calls %v", fake.recorded())
	}
}

// TestEnsureMachineIdentityLeavesAJWTApplicationAlone is the other half: an
// identity already provisioned correctly is read back without being rewritten,
// so a re-provision is a pure read.
func TestEnsureMachineIdentityLeavesAJWTApplicationAlone(t *testing.T) {
	fake := newFakeZitadel()
	fake.projects[machineIdentityProjectName] = "project-1"
	fake.apps["project-1\x00erun-env-0192"] = fakeApp{
		id:             "app-erun-env-0192",
		accessTokenTyp: apiTokenTypeJWT,
		clientID:       "client-erun-env-0192",
		clientSecret:   "secret-erun-env-0192",
	}
	client, _ := newTestClient(t, fake.handler())

	if _, err := client.EnsureMachineIdentity(context.Background(), EnsureMachineIdentityParams{OrgID: "org-1", LoginName: "erun-env-0192"}); err != nil {
		t.Fatalf("EnsureMachineIdentity: %v", err)
	}
	if corrections := fake.countContains("PUT /management/v1/projects"); corrections != 0 {
		t.Fatalf("expected no write for an already-correct identity, got calls %v", fake.recorded())
	}
}

// TestEnsureMachineIdentityRefusesAnApplicationWithoutCredentials guards the
// silent failure a mismatched binding would otherwise produce: enrolling an
// identity under an empty subject is an identity no token can ever resolve.
func TestEnsureMachineIdentityRefusesAnApplicationWithoutCredentials(t *testing.T) {
	fake := newFakeZitadel()
	fake.projects[machineIdentityProjectName] = "project-1"
	fake.apps["project-1\x00erun-env-0192"] = fakeApp{
		id:             "app-erun-env-0192",
		accessTokenTyp: apiTokenTypeJWT,
	}
	client, _ := newTestClient(t, fake.handler())

	if _, err := client.EnsureMachineIdentity(context.Background(), EnsureMachineIdentityParams{OrgID: "org-1", LoginName: "erun-env-0192"}); err == nil {
		t.Fatal("expected an error for an application reporting no client credentials")
	}
}

// TestEnsureMachineIdentityRefusesAnEmptyLoginName: the login name is the
// idempotency key, so provisioning without one could never be idempotent and
// must not reach the provider at all.
func TestEnsureMachineIdentityRefusesAnEmptyLoginName(t *testing.T) {
	fake := newFakeZitadel()
	client, _ := newTestClient(t, fake.handler())

	if _, err := client.EnsureMachineIdentity(context.Background(), EnsureMachineIdentityParams{OrgID: "org-1", LoginName: "  "}); err == nil {
		t.Fatal("expected an error for an empty login name")
	}
	if calls := fake.recorded(); len(calls) != 0 {
		t.Fatalf("expected no provider calls, got %v", calls)
	}
}

// TestDeleteMachineIdentityRemovesTheApplicationsCredential is the whole point
// of the call: the application is gone, so the client secret it carried can no
// longer mint a token, and the call reports that it removed one.
func TestDeleteMachineIdentityRemovesTheApplicationsCredential(t *testing.T) {
	fake := newFakeZitadel()
	client, _ := newTestClient(t, fake.handler())
	ctx := context.Background()
	if _, err := client.EnsureMachineIdentity(ctx, EnsureMachineIdentityParams{OrgID: "org-1", LoginName: "erun-env-0192"}); err != nil {
		t.Fatalf("EnsureMachineIdentity: %v", err)
	}

	removed, err := client.DeleteMachineIdentity(ctx, DeleteMachineIdentityParams{OrgID: "org-1", LoginName: "erun-env-0192"})
	if err != nil {
		t.Fatalf("DeleteMachineIdentity: %v", err)
	}
	if !removed {
		t.Fatal("expected the existing identity to be reported as removed")
	}
	if fake.holdsApp("project-"+machineIdentityProjectName, "erun-env-0192") {
		t.Fatal("the application is still in the project, so its client secret can still mint tokens")
	}
}

// TestDeleteMachineIdentityIsIdempotent: revocation runs inside a workflow that
// retries a failed attempt from the top, so a second call must report that
// there was nothing left rather than erroring on the absence it was asked to
// produce.
func TestDeleteMachineIdentityIsIdempotent(t *testing.T) {
	fake := newFakeZitadel()
	client, _ := newTestClient(t, fake.handler())
	ctx := context.Background()
	if _, err := client.EnsureMachineIdentity(ctx, EnsureMachineIdentityParams{OrgID: "org-1", LoginName: "erun-env-0192"}); err != nil {
		t.Fatalf("EnsureMachineIdentity: %v", err)
	}
	if _, err := client.DeleteMachineIdentity(ctx, DeleteMachineIdentityParams{OrgID: "org-1", LoginName: "erun-env-0192"}); err != nil {
		t.Fatalf("first DeleteMachineIdentity: %v", err)
	}

	removed, err := client.DeleteMachineIdentity(ctx, DeleteMachineIdentityParams{OrgID: "org-1", LoginName: "erun-env-0192"})
	if err != nil {
		t.Fatalf("second DeleteMachineIdentity: %v", err)
	}
	if removed {
		t.Fatal("a second revocation reported removing an identity that was already gone")
	}
}

// TestDeleteMachineIdentityDoesNotCreateTheProjectItWasLookingFor: revocation
// asks where the machine-identity project is, and the answer "nowhere" must
// stay that answer. A delete that conjures the container it was trying to empty
// is worse than one that finds nothing — it leaves the platform with a project
// that only ever existed because something was revoked.
func TestDeleteMachineIdentityDoesNotCreateTheProjectItWasLookingFor(t *testing.T) {
	fake := newFakeZitadel()
	client, _ := newTestClient(t, fake.handler())

	removed, err := client.DeleteMachineIdentity(context.Background(), DeleteMachineIdentityParams{OrgID: "org-1", LoginName: "erun-env-0192"})
	if err != nil {
		t.Fatalf("DeleteMachineIdentity: %v", err)
	}
	if removed {
		t.Fatal("reported removed an identity in a project that does not exist")
	}
	if creates := fake.countExact("POST /management/v1/projects"); creates != 0 {
		t.Fatalf("revocation created %d projects, calls %v", creates, fake.recorded())
	}
	if _, ok := fake.projects[machineIdentityProjectName]; ok {
		t.Fatal("revocation created the machine-identity project")
	}
}
