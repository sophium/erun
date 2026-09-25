package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// One credential's worth of the machine-identity flow, shared by every scenario
// that drives it: the platform API that mints the identity, and the tenant's own
// issuer that has to accept the credential it minted. Both halves live on one
// server because the identity the platform mints names the issuer its tokens
// come from, so a scenario that pointed them at two hosts would be exercising a
// shape that cannot happen.
const (
	machineIdentityClientID     = "machine-client"
	machineIdentityClientSecret = "machine-client-secret"
	machineIdentitySubject      = "machine-env-1"
	machineIdentityAccessToken  = "machine-access-token"
)

// hostPlatformAccessToken is the bearer the host's own signed-in alias presents,
// matching platform_test.go's requireBearer: the mint is made as that caller, so
// a scenario proves the platform client attached the host's credential rather
// than the environment's.
const hostPlatformAccessToken = "test-access-token"

// machineIdentityRefusal is the status and {code, message} envelope the platform
// answers a refused call with.
type machineIdentityRefusal struct {
	Status int
	Code   string
}

// machineIdentityTokenRequest is one token request the stub issuer answered.
type machineIdentityTokenRequest struct {
	GrantType string
	Scope     string
	// BasicAuth carries the client credential when the request presented it in
	// the Authorization header, as the client_credentials grant erun provisions
	// does; FormCredentials carries whatever it put in the body instead.
	BasicAuth       string
	FormCredentials string
}

// machineIdentityStub is what a scenario varies about the double below, and what
// it observes from it. Every field's zero value is the cooperating answer, so a
// scenario asserting the happy path names nothing.
type machineIdentityStub struct {
	// Environments, when non-nil, answers GET /v1/environments instead of the
	// default single row naming the environment under test. A non-nil empty
	// slice means the platform knows of no environment at all.
	Environments []map[string]any
	// MintRefusal, when set, is what the machine-identity mint answers instead of
	// an identity.
	MintRefusal *machineIdentityRefusal
	// MintOmitCredential makes the mint answer an identity with no client
	// credential -- a platform that recorded the identity but could not return
	// its secret, which is the one shape a caller has to refuse rather than
	// write down.
	MintOmitCredential bool
	// TokenRefusal, when set, is the status the token endpoint answers instead of
	// a token.
	TokenRefusal int
	// RejectOrgScope makes the token endpoint refuse the org-claim scope once
	// before accepting the retry, which is what an issuer that has never heard of
	// the scope does.
	RejectOrgScope bool
	// RequireClientAuthOnToken makes the token endpoint demand the credential in
	// the Authorization header. It is opt-in because the failure it pins --
	// quietly moving a long-lived secret into a request body -- is invisible in
	// the control flow's own trace, so only the scenario about it needs it.
	RequireClientAuthOnToken bool

	mu            sync.Mutex
	mints         int
	tokenRequests []machineIdentityTokenRequest
}

// machineIdentityServer runs the double. Every platform route authenticates with
// the host's own signed-in bearer, and the token endpoint issues the access
// token the API's own routes then require, so a scenario proves the credential
// was exchanged for a token and that the token was attached to the call.
func machineIdentityServer(t testing.TB, stub *machineIdentityStub) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/environments", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		environments := stub.Environments
		if environments == nil {
			environments = []map[string]any{{"environmentId": "env-1", "name": "dev", "status": "running"}}
		}
		_ = json.NewEncoder(w).Encode(environments)
	})

	mux.HandleFunc("POST /v1/environments/{environment_id}/machine-identity", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		stub.mu.Lock()
		stub.mints++
		refusal := stub.MintRefusal
		stub.mu.Unlock()
		if refusal != nil {
			w.WriteHeader(refusal.Status)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"code":    refusal.Code,
				"message": "the platform would not mint a machine identity for this tenant",
			})
			return
		}
		// The issuer is this same server: the platform mints an environment's
		// identity on its tenant's own issuer, so the credential it hands back has
		// to be exchangeable there and nowhere else.
		identity := map[string]any{
			"environmentId":   r.PathValue("environment_id"),
			"environmentName": "dev",
			"issuer":          "http://" + r.Host,
			"subject":         machineIdentitySubject,
			"clientId":        machineIdentityClientID,
			"clientSecret":    machineIdentityClientSecret,
			"userId":          "user-1",
			"alreadyEnrolled": false,
		}
		if stub.MintOmitCredential {
			delete(identity, "clientId")
			delete(identity, "clientSecret")
		}
		_ = json.NewEncoder(w).Encode(identity)
	})

	// The issuer half: discovery, then the grant itself. The API resolving a
	// tenant from a client-credentials token is why the org claim scope is
	// requested, and the endpoint is what decides whether it knows that scope.
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":         "http://" + r.Host,
			"token_endpoint": "http://" + r.Host + "/oauth/token",
		})
	})
	mux.HandleFunc("POST /oauth/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		if stub.RequireClientAuthOnToken {
			user, secret, ok := r.BasicAuth()
			if !ok || user != machineIdentityClientID || secret != machineIdentityClientSecret {
				http.Error(w, "client credentials must travel in the Authorization header", http.StatusUnauthorized)
				return
			}
		}
		request := machineIdentityTokenRequest{GrantType: r.PostForm.Get("grant_type"), Scope: r.PostForm.Get("scope")}
		if user, secret, ok := r.BasicAuth(); ok {
			request.BasicAuth = user + ":" + secret
		}
		request.FormCredentials = r.PostForm.Get("client_secret")
		stub.mu.Lock()
		stub.tokenRequests = append(stub.tokenRequests, request)
		refuseScope := stub.RejectOrgScope && request.Scope != "" && len(stub.tokenRequests) == 1
		stub.mu.Unlock()
		if refuseScope {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_scope", "error_description": "unknown scope"})
			return
		}
		if stub.TokenRefusal != 0 {
			w.WriteHeader(stub.TokenRefusal)
			_, _ = w.Write([]byte(`{"error":"invalid_client","error_description":"the credential was refused"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": machineIdentityAccessToken, "expires_in": 3600})
	})

	mux.HandleFunc("GET /v1/whoami", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+machineIdentityAccessToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"tenantId": "tenant-1", "tenantName": "acme", "userId": "user-1",
			"username": machineIdentitySubject, "issuer": "http://" + r.Host, "subject": machineIdentitySubject,
		})
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// tokenRequestLog returns the token requests the stub issuer has answered so far.
func (s *machineIdentityStub) tokenRequestLog() []machineIdentityTokenRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]machineIdentityTokenRequest(nil), s.tokenRequests...)
}

// mintCount returns how many machine-identity mint calls the platform received.
func (s *machineIdentityStub) mintCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mints
}

// removeCloudSecret deletes the file a secret ref resolves to, leaving any alias
// entry pointing at a credential that is no longer there. The ref is hashed the
// way erun-common's file-backed store names it, so this reaches the same file
// production reads.
func removeCloudSecret(t testing.TB, setup env.Setup, ref string) {
	t.Helper()
	sum := sha256.Sum256([]byte(ref))
	path := filepath.Join(setup.ConfigHome, "erun", "cloud-secrets", hex.EncodeToString(sum[:])+".token")
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove cloud secret %s: %v", path, err)
	}
}

// seedHostPlatformAccessToken seeds the cached access token the host's own
// signed-in alias presents to the platform, so the mint call authenticates as
// that caller without a refresh-token round trip this suite covers elsewhere.
func seedHostPlatformAccessToken(t testing.TB, setup env.Setup, alias string) {
	t.Helper()
	seedCachedERunAccessToken(t, setup, alias, hostPlatformAccessToken)
}

// TestMachineIdentityPlatformAlias covers the client half of a provisioned
// machine identity: the alias a pod carries once `erun init` minted it one.
// Where the init-side scenarios assert what such an environment is given, these
// assert what it does with it -- authenticate as itself, through the
// client_credentials grant, with no signed-in human anywhere in the flow.
func TestMachineIdentityPlatformAlias(t *testing.T) {
	t.Parallel()

	t.Run("real_run_mints_a_token_through_client_credentials", func(t *testing.T) {
		// The pod's own case. The alias carries a client-secret reference and no
		// refresh-token reference, so the only way it can authenticate is by
		// presenting that secret to the issuer's token endpoint -- and no cached
		// access token is seeded, so nothing upstream can answer for it. The
		// endpoint demands the credential in the Authorization header, which is
		// the half of Zitadel's BASIC client-auth method a trace line cannot show.
		stub := &machineIdentityStub{RequireClientAuthOnToken: true}
		server := machineIdentityServer(t, stub)
		setup := env.New(t)
		fixture.SeedMachineIdentityAlias(t, setup, "team", "erun+test@erun", server.URL, machineIdentityClientID, machineIdentityClientSecret)

		result := erun.Run(t, []string{"platform", "whoami"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		requests := stub.tokenRequestLog()
		if len(requests) != 1 {
			t.Fatalf("want exactly one token request, got %d: %+v", len(requests), requests)
		}
		if requests[0].GrantType != "client_credentials" {
			t.Fatalf("want the client_credentials grant, got %q", requests[0].GrantType)
		}
		if requests[0].FormCredentials != "" {
			t.Fatalf("the client secret must not travel in the request body: %+v", requests[0])
		}
		golden.Equal(t, "platform/machine_identity_whoami_real_run", normalize.Apply(result.Combined, stubServerRule(server, "<IDENTITY_API>")))
	})

	t.Run("real_run_reports_a_missing_credential_by_name", func(t *testing.T) {
		// A machine identity whose stored client secret is gone. `cloud login`
		// cannot fix it -- there is no session to refresh and no human it belongs
		// to -- so the failure has to name the one remedy that does, rather than
		// sending the operator to a sign-in that would leave the alias untouched.
		stub := &machineIdentityStub{}
		server := machineIdentityServer(t, stub)
		setup := env.New(t)
		fixture.SeedMachineIdentityAlias(t, setup, "team", "erun+test@erun", server.URL, machineIdentityClientID, machineIdentityClientSecret)
		removeCloudSecret(t, setup, "erun/clientsecret/erun+test@erun")

		result := erun.Run(t, []string{"platform", "whoami"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("a machine identity with no stored credential must not authenticate: %s", result.Combined)
		}
		if !strings.Contains(result.Combined, "re-provision") {
			t.Fatalf("the failure must name re-provisioning as the remedy: %s", result.Combined)
		}
		if len(stub.tokenRequestLog()) != 0 {
			t.Fatalf("a credential that could not be loaded must not reach the issuer: %+v", stub.tokenRequestLog())
		}
		golden.Equal(t, "platform/machine_identity_missing_credential_real_run", normalize.Apply(result.Combined, stubServerRule(server, "<IDENTITY_API>")))
	})

	t.Run("real_run_names_a_credential_the_issuer_refused", func(t *testing.T) {
		// The identity exists and its secret is stored, but the issuer will not
		// mint a token from it. The failure has to name the grant that failed
		// rather than read as a generic platform error: an operator cannot tell a
		// refused client credential from an unreachable plane by the status alone.
		stub := &machineIdentityStub{TokenRefusal: http.StatusUnauthorized}
		server := machineIdentityServer(t, stub)
		setup := env.New(t)
		fixture.SeedMachineIdentityAlias(t, setup, "team", "erun+test@erun", server.URL, machineIdentityClientID, machineIdentityClientSecret)

		result := erun.Run(t, []string{"platform", "whoami"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("a credential the issuer refuses must not authenticate: %s", result.Combined)
		}
		if !strings.Contains(result.Combined, "mint machine identity token") {
			t.Fatalf("the failure must name the grant that failed: %s", result.Combined)
		}
		golden.Equal(t, "platform/machine_identity_refused_credential_real_run", normalize.Apply(result.Combined, stubServerRule(server, "<IDENTITY_API>")))
	})

	t.Run("real_run_retries_an_issuer_that_rejects_the_org_scope", func(t *testing.T) {
		// erun's own IdP is org-scoped, so the grant asks for the org claim and
		// the tenant is resolved from it. A tenant's BYO issuer has never heard of
		// that scope, and a machine identity minted there has to keep working: the
		// scope is dropped and the grant retried, rather than an environment
		// losing its platform access to a scope name it never chose.
		stub := &machineIdentityStub{RejectOrgScope: true}
		server := machineIdentityServer(t, stub)
		setup := env.New(t)
		fixture.SeedMachineIdentityAlias(t, setup, "team", "erun+test@erun", server.URL, machineIdentityClientID, machineIdentityClientSecret)

		result := erun.Run(t, []string{"platform", "whoami"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		requests := stub.tokenRequestLog()
		if len(requests) != 2 {
			t.Fatalf("want the refusing scope followed by one retry, got %d requests: %+v", len(requests), requests)
		}
		if requests[0].Scope == "" || requests[1].Scope != "" {
			t.Fatalf("want the org-claim scope dropped on the retry, got %+v", requests)
		}
		golden.Equal(t, "platform/machine_identity_org_scope_retry_real_run", normalize.Apply(result.Combined, stubServerRule(server, "<IDENTITY_API>")))
	})
}
