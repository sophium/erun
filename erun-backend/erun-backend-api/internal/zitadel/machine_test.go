package zitadel

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakeZitadel records every call so a test can assert what a second pass did not do.
type fakeZitadel struct {
	mu       sync.Mutex
	projects map[string]string
	apps     map[string]fakeApp
	calls    []string
	// rowCap > 0 caps an unfiltered search the way Zitadel v4.19.5+ caps an
	// unpaged v1 list at its MaxQueryLimit.
	rowCap int
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

// Exact, not prefix: "POST /management/v1/projects" would also match its "_search".
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

type fakeRoute struct {
	matches func(*http.Request) bool
	serve   func(http.ResponseWriter, *http.Request)
}

func (f *fakeZitadel) routes() []fakeRoute {
	return []fakeRoute{
		{postTo("/management/v1/projects/_search"), f.searchProjects},
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

func (f *fakeZitadel) searchProjects(w http.ResponseWriter, r *http.Request) {
	result := make([]map[string]string, 0, len(f.projects))
	for name, id := range f.projects {
		result = append(result, map[string]string{"id": id, "name": name})
	}
	writeFakeJSON(w, map[string]any{"result": f.narrow(r, result)})
}

// narrow applies a request's exact-name query, then the row cap to an unfiltered
// one, over a name-ordered result so a capped page is deterministic.
func (f *fakeZitadel) narrow(r *http.Request, result []map[string]string) []map[string]string {
	var body struct {
		Queries []struct {
			NameQuery *struct {
				Name   string `json:"name"`
				Method string `json:"method"`
			} `json:"nameQuery"`
		} `json:"queries"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	sort.Slice(result, func(i, j int) bool { return result[i]["name"] < result[j]["name"] })
	for _, query := range body.Queries {
		if query.NameQuery == nil || query.NameQuery.Method != "TEXT_QUERY_METHOD_EQUALS" {
			continue
		}
		filtered := make([]map[string]string, 0, 1)
		for _, item := range result {
			if item["name"] == query.NameQuery.Name {
				filtered = append(filtered, item)
			}
		}
		return filtered
	}
	if f.rowCap > 0 && len(result) > f.rowCap {
		return result[:f.rowCap]
	}
	return result
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
	writeFakeJSON(w, map[string]any{"result": f.narrow(r, result)})
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

func (f *fakeZitadel) holdsApp(projectID string, name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.apps[projectID+"\x00"+name]
	return ok
}

func writeFakeJSON(w http.ResponseWriter, body any) {
	if err := json.NewEncoder(w).Encode(body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func projectIDFromPath(path string) string {
	rest := strings.TrimPrefix(path, "/management/v1/projects/")
	projectID, _, _ := strings.Cut(rest, "/")
	return projectID
}

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

// An empty subject is an identity no token can resolve.
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

// Zitadel v4.19.5+ returns at most its MaxQueryLimit rows for an unfiltered v1
// search; an identity created after that many siblings must still be found.
func TestMachineIdentityIsFoundPastTheServerRowCap(t *testing.T) {
	fake := newFakeZitadel()
	fake.rowCap = 1
	client, _ := newTestClient(t, fake.handler())
	ctx := context.Background()
	for _, loginName := range []string{"erun-env-0001", "erun-env-0002", "erun-env-0002"} {
		if _, err := client.EnsureMachineIdentity(ctx, EnsureMachineIdentityParams{OrgID: "org-1", LoginName: loginName}); err != nil {
			t.Fatalf("EnsureMachineIdentity(%s): %v", loginName, err)
		}
	}
	if creates := fake.countContains("/apps/api"); creates != 2 {
		t.Fatalf("expected one application create per identity, got %d: re-provisioning past the row cap created a second application, calls %v", creates, fake.recorded())
	}

	removed, err := client.DeleteMachineIdentity(ctx, DeleteMachineIdentityParams{OrgID: "org-1", LoginName: "erun-env-0002"})
	if err != nil {
		t.Fatalf("DeleteMachineIdentity: %v", err)
	}
	if !removed || fake.holdsApp("project-"+machineIdentityProjectName, "erun-env-0002") {
		t.Fatalf("removed=%v: an identity past the row cap was not revoked, so its client secret can still mint tokens", removed)
	}
}
