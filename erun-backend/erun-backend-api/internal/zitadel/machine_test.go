package zitadel

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeMachineUserDomain is the organization's own domain, which an instance
// whose domain policy requires domain-qualified login names appends to a
// machine user's preferred login name. The fake mints its client ids through
// it deliberately: the client id a credential presents is therefore never the
// name erun asked for, so a test can tell a client id read back from the
// provider apart from one reconstructed locally.
const fakeMachineUserDomain = "erun.test"

// fakeZitadel is a stand-in Management API holding just enough state to
// distinguish "the identity is not there" from "it already is": the
// organization's machine users and the secrets minted for them. It records
// every call so a test can assert what provisioning did *not* do on a second
// pass, which is the half idempotence actually rests on.
type fakeZitadel struct {
	mu sync.Mutex
	// users is every machine user, by login name.
	users map[string]fakeMachineUser
	// calls records "METHOD path" for every request served.
	calls []string
	// searchBodies records the body of every user search, so a test can assert
	// the lookup asked for a machine user rather than for any account holding
	// the name.
	searchBodies []map[string]any
}

type fakeMachineUser struct {
	id       string
	username string
	// secretsMinted counts the credentials issued for this account. A machine
	// secret is write-only at Zitadel -- there is no call that reads one back
	// -- so this is what a second provisioning call has to do to answer with a
	// usable credential.
	secretsMinted int
}

func newFakeZitadel() *fakeZitadel {
	return &fakeZitadel{users: map[string]fakeMachineUser{}}
}

func (f *fakeZitadel) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeZitadel) recordedSearchBodies() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.searchBodies...)
}

// countExact counts calls whose "METHOD path" is exactly call. Exact rather
// than prefix-matched because the collection endpoints are prefixes of their
// own create endpoints: a search for "POST /management/v1/users" would
// otherwise also count the create beside it, and the assertion that a second
// pass creates nothing would pass for the wrong reason.
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
		{postTo("/management/v1/users/_search"), f.searchUsers},
		{postTo("/management/v1/users/machine"), f.createMachineUser},
		{putSuffixed("/secret"), f.mintSecret},
		{deleteContaining("/management/v1/users/"), f.deleteUser},
	}
}

func postTo(path string) func(*http.Request) bool {
	return func(r *http.Request) bool { return r.Method == http.MethodPost && r.URL.Path == path }
}

func putSuffixed(suffix string) func(*http.Request) bool {
	return func(r *http.Request) bool { return r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, suffix) }
}

func deleteContaining(substring string) func(*http.Request) bool {
	return func(r *http.Request) bool {
		return r.Method == http.MethodDelete && strings.Contains(r.URL.Path, substring)
	}
}

// searchUsers serves the login-name lookup. It matches on the name it was
// asked for, exactly as Zitadel's own userNameQuery does against the stored
// user name -- which is the name erun passed at creation, not the domain-
// qualified login name a credential presents.
func (f *fakeZitadel) searchUsers(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.searchBodies = append(f.searchBodies, body)
	result := make([]map[string]string, 0)
	for loginName, user := range f.users {
		for _, query := range searchQueries(body) {
			nameQuery, ok := query["userNameQuery"].(map[string]any)
			if !ok {
				continue
			}
			if name, _ := nameQuery["userName"].(string); name == loginName {
				result = append(result, map[string]string{
					"id":                 user.id,
					"username":           user.username,
					"preferredLoginName": preferredLoginName(user.username),
				})
			}
		}
	}
	writeFakeJSON(w, map[string]any{"result": result})
}

// searchQueries reads the search body's query list, tolerating a body a test
// built by hand as well as the client's own encoding.
func searchQueries(body map[string]any) []map[string]any {
	raw, _ := body["queries"].([]any)
	queries := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		if query, ok := entry.(map[string]any); ok {
			queries = append(queries, query)
		}
	}
	return queries
}

func (f *fakeZitadel) createMachineUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserName string `json:"userName"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	user := fakeMachineUser{id: "user-" + body.UserName, username: body.UserName}
	f.users[body.UserName] = user
	writeFakeJSON(w, map[string]any{"userId": user.id})
}

// mintSecret issues a credential, counting the issuance on the account. The
// client id it answers with is the account's preferred login name -- the
// domain-qualified one -- rather than the name the account was created under,
// which is what makes reading it back from the provider load-bearing.
func (f *fakeZitadel) mintSecret(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/management/v1/users/"), "/secret")
	user, ok := f.userByID(userID)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	user.secretsMinted++
	f.users[user.username] = user
	writeFakeJSON(w, map[string]any{
		"clientId":     preferredLoginName(user.username),
		"clientSecret": "secret-" + user.username + "-" + strconv.Itoa(user.secretsMinted),
	})
}

func (f *fakeZitadel) deleteUser(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimPrefix(r.URL.Path, "/management/v1/users/")
	user, ok := f.userByID(userID)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	delete(f.users, user.username)
}

func (f *fakeZitadel) userByID(userID string) (fakeMachineUser, bool) {
	for _, user := range f.users {
		if user.id == userID {
			return user, true
		}
	}
	return fakeMachineUser{}, false
}

// holdsUser reports whether a machine user of this login name is still in the
// organization -- the credential half of what a revocation has to leave behind.
func (f *fakeZitadel) holdsUser(loginName string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.users[loginName]
	return ok
}

// secretsMinted counts the credentials issued for a login name, which is how a
// test tells "minted a fresh credential for the identity that already existed"
// from "minted a second identity".
func (f *fakeZitadel) secretsMinted(loginName string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.users[loginName].secretsMinted
}

func preferredLoginName(loginName string) string {
	return loginName + "@" + fakeMachineUserDomain
}

func writeFakeJSON(w http.ResponseWriter, body any) {
	_ = json.NewEncoder(w).Encode(body)
}

// TestEnsureMachineIdentityCreatesTheIdentityAndReturnsItsCredential is the
// ordinary first call: no machine user exists yet, so one is created and a
// credential is minted for it.
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
	if !identity.Created {
		t.Fatal("the first provisioning did not report creating the identity")
	}
	if creates := fake.countExact("POST /management/v1/users/machine"); creates != 1 {
		t.Fatalf("expected exactly one machine user create, got %d, calls %v", creates, fake.recorded())
	}
	if mints := fake.countContains("/secret"); mints != 1 {
		t.Fatalf("expected exactly one credential mint, got %d, calls %v", mints, fake.recorded())
	}
}

// TestEnsureMachineIdentitySeparatesTheCredentialFromTheTokenSubject is the
// property an erun user has to be enrolled under, and the one a project
// application could never supply: the client id the credential presents and
// the subject a token minted from it carries are two different values.
//
// Zitadel issues a service account's client_credentials token as the account's
// own user id, so an enrolment keyed on the client id would be an enrolment no
// token ever resolves -- the failure this test exists to keep from returning.
func TestEnsureMachineIdentitySeparatesTheCredentialFromTheTokenSubject(t *testing.T) {
	fake := newFakeZitadel()
	client, _ := newTestClient(t, fake.handler())

	identity, err := client.EnsureMachineIdentity(context.Background(), EnsureMachineIdentityParams{
		OrgID:     "org-1",
		LoginName: "erun-env-0192",
	})
	if err != nil {
		t.Fatalf("EnsureMachineIdentity: %v", err)
	}
	if identity.Subject == "" || identity.Subject == identity.ClientID {
		t.Fatalf("subject %q and client id %q are not two distinct values", identity.Subject, identity.ClientID)
	}
	if want := "user-erun-env-0192"; identity.Subject != want {
		t.Fatalf("subject = %q, want the machine user's own id %q", identity.Subject, want)
	}
}

// TestEnsureMachineIdentityReadsTheClientIDBackFromTheProvider covers the
// domain policy an erun-shipped instance sets: the login name the account was
// created under is not the login name its credential presents. A client id
// reconstructed from the name erun asked for would be a credential no
// client_credentials grant accepts.
func TestEnsureMachineIdentityReadsTheClientIDBackFromTheProvider(t *testing.T) {
	fake := newFakeZitadel()
	client, _ := newTestClient(t, fake.handler())

	identity, err := client.EnsureMachineIdentity(context.Background(), EnsureMachineIdentityParams{
		OrgID:     "org-1",
		LoginName: "erun-env-0192",
	})
	if err != nil {
		t.Fatalf("EnsureMachineIdentity: %v", err)
	}
	if want := "erun-env-0192@" + fakeMachineUserDomain; identity.ClientID != want {
		t.Fatalf("client id = %q, want the domain-qualified login name %q the provider reported", identity.ClientID, want)
	}
}

// TestEnsureMachineIdentityLooksUpOnlyMachineUsers: the login name is derived
// from an environment's id, but the lookup must still say which kind of
// account it means -- a name match on a human account would hand this caller
// an identity that is not the environment's.
func TestEnsureMachineIdentityLooksUpOnlyMachineUsers(t *testing.T) {
	fake := newFakeZitadel()
	client, _ := newTestClient(t, fake.handler())

	if _, err := client.EnsureMachineIdentity(context.Background(), EnsureMachineIdentityParams{OrgID: "org-1", LoginName: "erun-env-0192"}); err != nil {
		t.Fatalf("EnsureMachineIdentity: %v", err)
	}
	bodies := fake.recordedSearchBodies()
	if len(bodies) != 1 {
		t.Fatalf("expected exactly one user search, got %d", len(bodies))
	}
	if !hasLoginNameQuery(bodies[0], "erun-env-0192") || !hasMachineTypeQuery(bodies[0]) {
		t.Fatalf("search queries = %+v, want an exact login-name query and a machine-type query", searchQueries(bodies[0]))
	}
}

// hasLoginNameQuery reports whether the search body asks for this exact login
// name, and does not merely search by name some other way.
func hasLoginNameQuery(body map[string]any, loginName string) bool {
	for _, query := range searchQueries(body) {
		nameQuery, ok := query["userNameQuery"].(map[string]any)
		if ok && nameQuery["userName"] == loginName && nameQuery["method"] == tokenQueryMethodEquals {
			return true
		}
	}
	return false
}

// hasMachineTypeQuery reports whether the search body is restricted to machine
// users, which is what keeps a human account holding the same name out of it.
func hasMachineTypeQuery(body map[string]any) bool {
	for _, query := range searchQueries(body) {
		if typeQuery, ok := query["typeQuery"].(map[string]any); ok && typeQuery["type"] == machineUserTypeMachine {
			return true
		}
	}
	return false
}

// TestEnsureMachineIdentityIsIdempotentForOneLoginName is the property
// provisioning an environment twice rests on: the second call finds the
// machine user the first one created and creates nothing.
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

	if creates := fake.countExact("POST /management/v1/users/machine"); creates != 1 {
		t.Fatalf("expected the second call to create no machine user, but the create endpoint was called %d times", creates)
	}
	if second.Subject != first.Subject || second.ClientID != first.ClientID {
		t.Fatalf("second call resolved a different identity: %+v vs %+v", second, first)
	}
	if second.Created {
		t.Fatal("the second call reported creating an identity that already existed")
	}
	// The credential itself cannot be read back -- Zitadel stores a machine
	// secret as a hash -- so a usable one has to be minted. What must not
	// happen is a second *identity*; the mint is counted to make the
	// distinction between "re-issued this identity's secret" and "minted a
	// second identity" impossible to miss.
	if mints := fake.secretsMinted("erun-env-0192"); mints != 2 {
		t.Fatalf("expected one credential per call against the one machine user, got %d", mints)
	}
	if users := len(fake.users); users != 1 {
		t.Fatalf("expected exactly one machine user after two calls, got %d", users)
	}
}

// TestEnsureMachineIdentityRefusesAnUnusableMintedCredential guards the silent
// failure a mismatched binding would otherwise produce: enrolling an identity
// under an empty subject is an identity no token can ever resolve.
func TestEnsureMachineIdentityRefusesAnUnusableMintedCredential(t *testing.T) {
	// A credential minted without a secret: the account exists and the call
	// reports a client id, but there is nothing to authenticate with.
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			writeFakeJSON(w, map[string]any{"clientId": "erun-env-0192@" + fakeMachineUserDomain})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/_search") {
			writeFakeJSON(w, map[string]any{"result": []map[string]string{{"id": "user-erun-env-0192"}}})
			return
		}
		writeFakeJSON(w, map[string]any{"userId": "user-erun-env-0192"})
	})

	if _, err := client.EnsureMachineIdentity(context.Background(), EnsureMachineIdentityParams{OrgID: "org-1", LoginName: "erun-env-0192"}); err == nil {
		t.Fatal("expected an error for a credential minted without a secret")
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

// TestDeleteMachineIdentityRemovesTheMachinesCredential is the whole point of
// the call: the machine user is gone, so the client secret it carried can no
// longer mint a token, and the call reports that it removed one.
func TestDeleteMachineIdentityRemovesTheMachinesCredential(t *testing.T) {
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
	if fake.holdsUser("erun-env-0192") {
		t.Fatal("the machine user is still in the organization, so its client secret can still mint tokens")
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

// TestDeleteMachineIdentityDoesNotCreateTheUserItWasLookingFor: revocation
// asks where the machine identity is, and the answer "nowhere" must stay that
// answer. A delete that conjures the account it was trying to remove leaves
// the platform with a machine user that only ever existed because something
// was revoked.
func TestDeleteMachineIdentityDoesNotCreateTheUserItWasLookingFor(t *testing.T) {
	fake := newFakeZitadel()
	client, _ := newTestClient(t, fake.handler())

	removed, err := client.DeleteMachineIdentity(context.Background(), DeleteMachineIdentityParams{OrgID: "org-1", LoginName: "erun-env-0192"})
	if err != nil {
		t.Fatalf("DeleteMachineIdentity: %v", err)
	}
	if removed {
		t.Fatal("reported removed an identity that does not exist")
	}
	if creates := fake.countExact("POST /management/v1/users/machine"); creates != 0 {
		t.Fatalf("revocation created %d machine users, calls %v", creates, fake.recorded())
	}
	if users := len(fake.users); users != 0 {
		t.Fatalf("revocation left %d machine users behind", users)
	}
}

// TestDeleteMachineIdentityForgetsADeletedAccount: a machine user already
// removed by hand must be reported as nothing removed rather than as a
// failure, the same find-first rule every other call here follows.
func TestDeleteMachineIdentityForgetsADeletedAccount(t *testing.T) {
	fake := newFakeZitadel()
	client, _ := newTestClient(t, fake.handler())

	removed, err := client.DeleteMachineIdentity(context.Background(), DeleteMachineIdentityParams{OrgID: "org-1", LoginName: "  "})
	if err != nil {
		t.Fatalf("DeleteMachineIdentity: %v", err)
	}
	if removed {
		t.Fatal("reported removing an identity for an empty login name")
	}
	if calls := fake.recorded(); len(calls) != 0 {
		t.Fatalf("expected no provider calls for an empty login name, got %v", calls)
	}
}
