package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
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

// The platform API and the tenant issuer share one server because a minted
// identity names the issuer its tokens come from.
const (
	machineIdentityClientID     = "machine-client"
	machineIdentityClientSecret = "machine-client-secret"
	machineIdentitySubject      = "machine-env-1"
	machineIdentityAccessToken  = "machine-access-token"
)

// hostPlatformAccessToken is the host alias's bearer, matching platform_test.go's requireBearer.
const hostPlatformAccessToken = "test-access-token"

type machineIdentityRefusal struct {
	Status int
	Code   string
}

type machineIdentityTokenRequest struct {
	GrantType string
	Scope     string
	// BasicAuth is set when the credential came in the Authorization header;
	// FormCredentials when it came in the body.
	BasicAuth       string
	FormCredentials string
}

// machineIdentityStub configures the double; zero values cooperate.
type machineIdentityStub struct {
	// Environments overrides GET /v1/environments; non-nil empty means none known.
	Environments []map[string]any
	MintRefusal  *machineIdentityRefusal
	// MintOmitCredential returns an identity without its secret, which callers must refuse.
	MintOmitCredential bool
	TokenRefusal       int
	// RejectOrgScope refuses the org-claim scope once, as a BYO issuer would.
	RejectOrgScope bool
	// RequireClientAuthOnToken demands the credential in the Authorization header;
	// moving it to the body is invisible in the trace.
	RequireClientAuthOnToken bool

	mu            sync.Mutex
	mints         int
	tokenRequests []machineIdentityTokenRequest
	buildReports  []string
}

// machineIdentityServer runs the double.
func machineIdentityServer(t testing.TB, stub *machineIdentityStub) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/environments", stub.environmentsHandler())
	mux.HandleFunc("POST /v1/environments/{environment_id}/machine-identity", stub.mintHandler())
	mux.HandleFunc("GET /.well-known/openid-configuration", stub.discoveryHandler())
	mux.HandleFunc("POST /oauth/token", stub.tokenHandler())
	mux.HandleFunc("GET /v1/whoami", stub.whoamiHandler())
	mux.HandleFunc("POST /v1/builds", stub.buildReportHandler())

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// Accepts both the host's bearer (`erun init`) and the environment's own token (build).
func (s *machineIdentityStub) environmentsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer " + hostPlatformAccessToken, "Bearer " + machineIdentityAccessToken:
		default:
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		environments := s.Environments
		if environments == nil {
			environments = []map[string]any{{"environmentId": "env-1", "name": "dev", "status": "running"}}
		}
		_ = json.NewEncoder(w).Encode(environments)
	}
}

func (s *machineIdentityStub) mintHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		s.mu.Lock()
		s.mints++
		refusal := s.MintRefusal
		s.mu.Unlock()
		if refusal != nil {
			w.WriteHeader(refusal.Status)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"code":    refusal.Code,
				"message": "the platform would not mint a machine identity for this tenant",
			})
			return
		}
		// The credential must be exchangeable only on the tenant's own issuer.
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
		if s.MintOmitCredential {
			delete(identity, "clientId")
			delete(identity, "clientSecret")
		}
		_ = json.NewEncoder(w).Encode(identity)
	}
}

func (s *machineIdentityStub) discoveryHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":         "http://" + r.Host,
			"token_endpoint": "http://" + r.Host + "/oauth/token",
		})
	}
}

func (s *machineIdentityStub) tokenHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		if s.RequireClientAuthOnToken && !presentsMachineClientAuth(r) {
			http.Error(w, "client credentials must travel in the Authorization header", http.StatusUnauthorized)
			return
		}
		request := machineIdentityTokenRequest{GrantType: r.PostForm.Get("grant_type"), Scope: r.PostForm.Get("scope")}
		if user, secret, ok := r.BasicAuth(); ok {
			request.BasicAuth = user + ":" + secret
		}
		request.FormCredentials = r.PostForm.Get("client_secret")
		s.mu.Lock()
		s.tokenRequests = append(s.tokenRequests, request)
		refuseScope := s.RejectOrgScope && request.Scope != "" && len(s.tokenRequests) == 1
		s.mu.Unlock()
		if refuseScope {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_scope", "error_description": "unknown scope"})
			return
		}
		if s.TokenRefusal != 0 {
			w.WriteHeader(s.TokenRefusal)
			_, _ = w.Write([]byte(`{"error":"invalid_client","error_description":"the credential was refused"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": machineIdentityAccessToken, "expires_in": 3600})
	}
}

func presentsMachineClientAuth(r *http.Request) bool {
	user, secret, ok := r.BasicAuth()
	return ok && user == machineIdentityClientID && secret == machineIdentityClientSecret
}

func (s *machineIdentityStub) whoamiHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+machineIdentityAccessToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"tenantId": "tenant-1", "tenantName": "acme", "userId": "user-1",
			"username": machineIdentitySubject, "issuer": "http://" + r.Host, "subject": machineIdentitySubject,
		})
	}
}

// Requires the machine credential specifically, so a fallback to the
// operator's delegated session fails here as it would in production.
func (s *machineIdentityStub) buildReportHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+machineIdentityAccessToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.buildReports = append(s.buildReports, string(body))
		s.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"buildId": "build-1", "successful": true})
	}
}

func (s *machineIdentityStub) tokenRequestLog() []machineIdentityTokenRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]machineIdentityTokenRequest(nil), s.tokenRequests...)
}

func (s *machineIdentityStub) mintCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mints
}

func (s *machineIdentityStub) buildReportLog() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.buildReports...)
}

// removeCloudSecret deletes the file a secret ref resolves to, hashed the way
// erun-common's file-backed store names it.
func removeCloudSecret(t testing.TB, setup env.Setup, ref string) {
	t.Helper()
	sum := sha256.Sum256([]byte(ref))
	path := filepath.Join(setup.ConfigHome, "erun", "cloud-secrets", hex.EncodeToString(sum[:])+".token")
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove cloud secret %s: %v", path, err)
	}
}

// seedHostPlatformAccessToken seeds the host alias's cached token to skip a refresh round trip.
func seedHostPlatformAccessToken(t testing.TB, setup env.Setup, alias string) {
	t.Helper()
	seedCachedERunAccessToken(t, setup, alias, hostPlatformAccessToken)
}

// TestMachineIdentityPlatformAlias covers a pod authenticating as its machine
// identity via client_credentials.
func TestMachineIdentityPlatformAlias(t *testing.T) {
	t.Parallel()

	t.Run("real_run_mints_a_token_through_client_credentials", testMachineIdentityPlatformAliasRealRunMintsATokenThroughClientCredentials)
	t.Run("real_run_reports_a_missing_credential_by_name", testMachineIdentityPlatformAliasRealRunReportsAMissingCredentialByName)
	t.Run("real_run_names_a_credential_the_issuer_refused", testMachineIdentityPlatformAliasRealRunNamesACredentialTheIssuerRefused)
	t.Run("real_run_retries_an_issuer_that_rejects_the_org_scope", testMachineIdentityPlatformAliasRealRunRetriesAnIssuerThatRejectsTheOrgScope)
}

func testMachineIdentityPlatformAliasRealRunMintsATokenThroughClientCredentials(t *testing.T) {
	// No cached token, so only the client secret can authenticate.
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
}

func testMachineIdentityPlatformAliasRealRunReportsAMissingCredentialByName(t *testing.T) {
	// The remedy named must not be `cloud login`, which cannot fix a machine identity.
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
}

func testMachineIdentityPlatformAliasRealRunNamesACredentialTheIssuerRefused(t *testing.T) {
	// The failure must name the grant, not read as a generic platform error.
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
}

func testMachineIdentityPlatformAliasRealRunRetriesAnIssuerThatRejectsTheOrgScope(t *testing.T) {
	// A BYO issuer does not know the org scope; it is dropped and the grant retried.
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
}

// TestMachineIdentityBuildSelfReport asserts `erun build` posts POST /v1/builds
// as the machine identity.
func TestMachineIdentityBuildSelfReport(t *testing.T) {
	t.Parallel()

	t.Run("real_run_reports_its_build_through_the_machine_identity", func(t *testing.T) {
		// The stub refuses POST /v1/builds to any other bearer.
		stub := &machineIdentityStub{Environments: []map[string]any{
			{"environmentId": "env-local", "name": "local", "status": "running"},
		}}
		server := machineIdentityServer(t, stub)
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		fixture.SeedTenantEnv(t, setup, "team", "local")
		fixture.SeedMachineIdentityAlias(t, setup, "team", "erun+test@erun", server.URL, machineIdentityClientID, machineIdentityClientSecret)

		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
			`case "$1" in`,
			`  image)`,
			`    case "$2" in`,
			`      inspect) exit 1 ;;`,
			`      *) exit 0 ;;`,
			`    esac`,
			`    ;;`,
			`  buildx)`,
			`    case "$2" in`,
			`      inspect) echo "Platforms: linux/arm64*, linux/amd64" ;;`,
			`      *) exit 0 ;;`,
			`    esac`,
			`    ;;`,
			`  *) exit 0 ;;`,
			`esac`,
		}, "\n"))
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
		envVars = append(envVars, stubHelmSilent(t, setup)...)

		result := erun.Run(t, []string{"build", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}

		reports := stub.buildReportLog()
		if len(reports) != 1 {
			t.Fatalf("want exactly one build self-report as the machine identity, got %d: %+v", len(reports), reports)
		}
		var report map[string]any
		if err := json.Unmarshal([]byte(reports[0]), &report); err != nil {
			t.Fatalf("decode build report %q: %v", reports[0], err)
		}
		if report["environmentId"] == "" || report["commitId"] == "" {
			t.Fatalf("the build report names no environment or commit: %s", reports[0])
		}
		if report["successful"] != true {
			t.Fatalf("the build succeeded but reported otherwise: %s", reports[0])
		}
		golden.Equal(t, "platform/machine_identity_build_self_report_real_run", normalize.Apply(result.Combined, stubServerRule(server, "<IDENTITY_API>")))
	})
}
