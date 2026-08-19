package eruncommon

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

func TestPlatformClientStatusErrorMapping(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, ErrPlatformUnauthorized},
		{http.StatusForbidden, ErrPlatformForbidden},
		{http.StatusNotFound, ErrPlatformNotFound},
		{http.StatusConflict, ErrPlatformConflict},
		{http.StatusNotImplemented, ErrPlatformNotImplemented},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "detail: "+http.StatusText(tc.status), tc.status)
			}))
			defer srv.Close()

			client := NewPlatformClient(srv.URL, staticToken("token-1"))
			_, err := client.Whoami(context.Background())
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want errors.Is match for %v", err, tc.want)
			}
			if !strings.Contains(err.Error(), "detail: "+http.StatusText(tc.status)) {
				t.Fatalf("err %v does not carry the server's plain-text detail", err)
			}
		})
	}
}

func TestPlatformClientUnrecognizedStatusIsAGenericError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1"))
	_, err := client.Whoami(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, sentinel := range []error{ErrPlatformUnauthorized, ErrPlatformForbidden, ErrPlatformNotFound, ErrPlatformConflict, ErrPlatformNotImplemented} {
		if errors.Is(err, sentinel) {
			t.Fatalf("err = %v matched sentinel %v, want no match for a 500", err, sentinel)
		}
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want to carry the server's message", err)
	}
}

func TestPlatformClientEmptyBodyResponseDecodesToZeroValue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1"))
	whoami, err := client.Whoami(context.Background())
	if err != nil {
		t.Fatalf("Whoami with empty body: %v", err)
	}
	if whoami.TenantID != "" || whoami.UserID != "" || len(whoami.Roles) != 0 {
		t.Fatalf("whoami = %+v, want zero value", whoami)
	}
}

func TestPlatformClientTrimsTrailingSlashFromBaseURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/platform" {
			t.Fatalf("path = %q, want exactly one leading slash", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(PlatformInfo{})
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL+"/", nil)
	if _, err := client.Platform(context.Background()); err != nil {
		t.Fatalf("Platform: %v", err)
	}
}
