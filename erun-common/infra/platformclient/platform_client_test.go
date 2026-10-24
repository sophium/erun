package platformclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func staticToken(token string) PlatformTokenMinter {
	return func() (string, error) { return token, nil }
}

func TestPlatformClientPlatformDoesNotAuthenticate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Fatal("GET /v1/platform must not send a bearer token")
		}
		_ = json.NewEncoder(w).Encode(PlatformInfo{Issuer: "https://auth.example.test", CLIClientID: "cli-1"})
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL, nil)
	info, err := client.Platform(context.Background())
	if err != nil {
		t.Fatalf("Platform: %v", err)
	}
	if info.Issuer != "https://auth.example.test" || info.CLIClientID != "cli-1" {
		t.Fatalf("info = %+v", info)
	}
}

func TestPlatformClientAuthenticatedCallSendsMintedBearer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Fatalf("Authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(PlatformWhoami{TenantID: "tenant-1", UserID: "user-1"})
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1"))
	whoami, err := client.Whoami(context.Background())
	if err != nil {
		t.Fatalf("Whoami: %v", err)
	}
	if whoami.TenantID != "tenant-1" || whoami.UserID != "user-1" {
		t.Fatalf("whoami = %+v", whoami)
	}
}

// TestPlatformClientWithMCPToolSendsAuditHeader pins the client side of
// AuditEventTypeMCP: erun-backend-api can only classify a call as MCP-driven
// if the request actually carries this header.
func TestPlatformClientWithMCPToolSendsAuditHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(MCPToolAuditHeader); got != "review_show" {
			t.Fatalf("%s = %q, want %q", MCPToolAuditHeader, got, "review_show")
		}
		_ = json.NewEncoder(w).Encode(PlatformWhoami{TenantID: "tenant-1"})
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1")).WithMCPTool("review_show")
	if _, err := client.Whoami(context.Background()); err != nil {
		t.Fatalf("Whoami: %v", err)
	}
}

// TestPlatformClientWithoutMCPToolSendsNoAuditHeader guards the CLI/library
// default: a caller that never opts in must not send the header at all, so
// erun-backend-api keeps classifying it as a plain API call.
func TestPlatformClientWithoutMCPToolSendsNoAuditHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(MCPToolAuditHeader); got != "" {
			t.Fatalf("%s = %q, want empty", MCPToolAuditHeader, got)
		}
		_ = json.NewEncoder(w).Encode(PlatformWhoami{TenantID: "tenant-1"})
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1"))
	if _, err := client.Whoami(context.Background()); err != nil {
		t.Fatalf("Whoami: %v", err)
	}
}

func TestPlatformClientAuthenticatedCallWithoutMinterFailsClearly(t *testing.T) {
	client := NewPlatformClient("https://api.example.test", nil)
	if _, err := client.Whoami(context.Background()); err == nil {
		t.Fatal("expected an error when no token minter is configured")
	} else if !strings.Contains(err.Error(), "token minter") {
		t.Fatalf("error = %v, want to mention the missing token minter", err)
	}
}

func TestPlatformClientMintErrorPropagates(t *testing.T) {
	client := NewPlatformClient("https://api.example.test", func() (string, error) {
		return "", errors.New("refresh failed")
	})
	if _, err := client.Whoami(context.Background()); err == nil || !strings.Contains(err.Error(), "refresh failed") {
		t.Fatalf("error = %v, want to mention the mint failure", err)
	}
}

func TestPlatformClientCreateUserSendsJSONBodyAndReturnsCreated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/users" {
			t.Fatalf("method=%s path=%s", r.Method, r.URL.Path)
		}
		var body PlatformCreateUserParams
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body.Username != "alice" || body.TenantID != "tenant-b" {
			t.Fatalf("body = %+v", body)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(PlatformUser{UserID: "user-1", TenantID: "tenant-b", Username: "alice"})
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1"))
	user, err := client.CreateUser(context.Background(), PlatformCreateUserParams{Username: "alice", TenantID: "tenant-b"})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if user.UserID != "user-1" || user.Username != "alice" {
		t.Fatalf("user = %+v", user)
	}
}

// TestPlatformClientCreateUserSendsRoleIDs is the cross-tenant recovery path:
// an operations caller enrolls a tenant's administrator directly, so the roles
// have to reach the platform on the enrollment itself rather than needing a
// later grant from inside a tenant that may have nobody able to make one.
func TestPlatformClientCreateUserSendsRoleIDs(t *testing.T) {
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(PlatformUser{UserID: "user-1", TenantID: "tenant-b", Username: "admin"})
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1"))
	if _, err := client.CreateUser(context.Background(), PlatformCreateUserParams{
		Username: "admin", TenantID: "tenant-b", RoleIDs: []string{"role-admin"},
	}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	roles, ok := raw["roleIds"].([]any)
	if !ok || len(roles) != 1 || roles[0] != "role-admin" {
		t.Fatalf("body = %+v, want roleIds=[role-admin]", raw)
	}
}

// An empty role list must stay off the wire entirely, so the platform applies
// its own default rather than seeing an explicit "grant nothing".
func TestPlatformClientCreateUserOmitsEmptyRoleIDs(t *testing.T) {
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(PlatformUser{UserID: "user-1", Username: "alice"})
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1"))
	if _, err := client.CreateUser(context.Background(), PlatformCreateUserParams{Username: "alice"}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, present := raw["roleIds"]; present {
		t.Fatalf("body = %+v, want no roleIds key", raw)
	}
}

func TestPlatformClientListUsersEncodesTenantIDQueryParam(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("tenantId") != "tenant-b" {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode([]PlatformUser{{UserID: "u1"}})
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1"))
	users, err := client.ListUsers(context.Background(), PlatformListUsersParams{TenantID: "tenant-b"})
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 1 || users[0].UserID != "u1" {
		t.Fatalf("users = %+v", users)
	}
}

func TestPlatformClientGetEnvironmentEscapesPathSegment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/v1/environments/env%201" {
			t.Errorf("escaped path = %q, want /v1/environments/env%%201", r.URL.EscapedPath())
		}
		_ = json.NewEncoder(w).Encode(PlatformEnvironment{EnvironmentID: "env 1"})
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1"))
	if _, err := client.GetEnvironment(context.Background(), "env 1"); err != nil {
		t.Fatalf("GetEnvironment: %v", err)
	}
}

func TestPlatformClientDeployEnvironment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/environments/env-1/deploy" || r.Method != http.MethodPost {
			t.Fatalf("method=%s path=%s", r.Method, r.URL.Path)
		}
		var body PlatformDeployEnvironmentParams
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Version != "1.2.3" {
			t.Fatalf("body = %+v", body)
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(PlatformEnvironment{EnvironmentID: "env-1", Status: "provisioning"})
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1"))
	environment, err := client.DeployEnvironment(context.Background(), "env-1", PlatformDeployEnvironmentParams{Version: "1.2.3"})
	if err != nil {
		t.Fatalf("DeployEnvironment: %v", err)
	}
	if environment.Status != "provisioning" {
		t.Fatalf("environment = %+v", environment)
	}
}

func TestPlatformClientCreateContextPreviewReturnsPlanOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body PlatformCreateContextParams
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !body.Preview {
			t.Fatal("expected preview=true in the request body")
		}
		_ = json.NewEncoder(w).Encode(PlatformCreateContextResult{Plan: []string{"step 1", "step 2"}})
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1"))
	result, err := client.CreateContext(context.Background(), PlatformCreateContextParams{
		Name: "ctx-1", CloudProviderAlias: "dev+123@aws", Region: "us-east-1", Preview: true,
	})
	if err != nil {
		t.Fatalf("CreateContext: %v", err)
	}
	if result.Context != nil || len(result.Plan) != 2 {
		t.Fatalf("result = %+v", result)
	}
}

func TestPlatformClientProvision(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/provision" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(PlatformProvisionResult{Plan: []string{"a", "b"}, QuotaOk: true})
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1"))
	result, err := client.Provision(context.Background(), PlatformProvisionParams{
		Environment: PlatformProvisionEnvironment{Name: "prod", Type: "runtime"},
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if !result.QuotaOk || len(result.Plan) != 2 {
		t.Fatalf("result = %+v", result)
	}
}
