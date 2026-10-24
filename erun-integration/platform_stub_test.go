package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
)

// seedCachedERunAccessToken writes a cached, unexpired access token directly
// (bypassing the OIDC device flow cloud_test.go's real-run scenarios already
// cover) so a platform scenario can hit the real erun-backend-api stub
// authenticated from its first request.
func seedCachedERunAccessToken(t testing.TB, setup env.Setup, alias, token string) {
	t.Helper()
	dir := filepath.Join(setup.ConfigHome, "erun", "cloud-secrets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	sum := sha256.Sum256([]byte("erun/access/" + alias))
	path := filepath.Join(dir, hex.EncodeToString(sum[:])+".token")
	body := `{"accessToken":"` + token + `","expiresAt":"2999-01-01T00:00:00Z"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write cached access token: %v", err)
	}
}

// platformAlias seeds a minimal erun-type cloud alias pointed at server, with
// a cached access token already in place — the alias `erun platform`
// commands resolve by default when exactly one erun-type alias is configured.
func platformAlias(t testing.TB, setup env.Setup, server *httptest.Server) string {
	t.Helper()
	alias := "erun+test@erun"
	seedERunCloudProviderAlias(t, setup, alias, server.URL, "cli-test-client")
	seedCachedERunAccessToken(t, setup, alias, "test-access-token")
	return alias
}

// requireBearer answers 401 unless the request carries the seeded test token,
// so a scenario proves the CLI actually attached the bearer it minted.
func requireBearer(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Authorization") != "Bearer test-access-token" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

// platformAPIStubServer runs a minimal erun-backend-api double covering every
// route `erun platform` drives, so real-run scenarios exercise
// platformclient's request/response handling end to end
// rather than only its --dry-run trace branch.
func platformAPIStubServer(t testing.TB) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	registerPlatformIdentityStubRoutes(mux)
	registerPlatformTenantStubRoutes(mux)
	registerPlatformEnvironmentStubRoutes(mux)
	registerPlatformDefinitionStubRoutes(mux)
	registerPlatformContextStubRoutes(mux)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func registerPlatformIdentityStubRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/whoami", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		// tenantName ("acme") deliberately matches GET /v1/tenants' own name for
		// tenant-1 below, and deliberately differs from username ("test-user") --
		// The defect was an operator reading whoami's username as if it were the
		// tenant's name, so this scenario proves the two stay visibly distinct.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tenantId": "tenant-1", "tenantName": "acme", "userId": "user-1", "username": "test-user", "issuer": "https://idp.example", "subject": "sub-1",
		})
	})
	mux.HandleFunc("POST /v1/identity/orgs", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "org-" + body["name"], "name": body["name"]})
	})
	// GET /v1/platform is unauthenticated on the real API (routes.RegisterPlatformRoute),
	// so this stub never calls requireBearer -- a scenario proving `platform
	// version` works even with no/expired credentials would otherwise pass for
	// the wrong reason if the stub itself demanded a bearer.
	mux.HandleFunc("GET /v1/platform", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": "https://idp.example", "apiUrl": "https://api.example.test", "version": "1.2.3",
		})
	})
	mux.HandleFunc("POST /v1/users", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"userId": "user-2", "tenantId": "tenant-1", "username": body["username"], "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z",
		})
	})
	mux.HandleFunc("GET /v1/users", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"userId": "user-1", "tenantId": "tenant-1", "username": "test-user", "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z"},
		})
	})
}

func registerPlatformTenantStubRoutes(mux *http.ServeMux) {
	// orgScopedIssuers mirrors the real backend's issuers.org_field_key: once
	// an issuer is registered org-scoped (the first POST /v1/tenants that
	// names an orgFieldKey for it), every later tenant on that issuer needs a
	// real orgFieldValue or it can never resolve. This is what lets the
	// identity-org-create scenarios prove the reachable path actually
	// produces a tenant that would resolve, not merely one that was accepted.
	var mu sync.Mutex
	orgScopedIssuers := map[string]string{}

	mux.HandleFunc("POST /v1/tenants", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)

		mu.Lock()
		if body["orgFieldKey"] != "" {
			if _, seen := orgScopedIssuers[body["issuer"]]; !seen {
				orgScopedIssuers[body["issuer"]] = body["orgFieldKey"]
			}
		}
		orgFieldKey := orgScopedIssuers[body["issuer"]]
		mu.Unlock()

		// Mirrors assertResolvableIssuerMapping: an org-scoped issuer with no
		// org value on this mapping can never resolve any token, so the real
		// backend refuses it rather than minting a dead tenant.
		if orgFieldKey != "" && body["orgFieldValue"] == "" {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    "UNRESOLVABLE_ISSUER_MAPPING",
				"message": "issuer resolves tenants by an org claim; create one with `erun platform identity org create --name <org-name>` and pass its id as --org-field-value",
			})
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tenantId": "tenant-2", "name": body["name"], "type": body["type"], "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z",
		})
	})
	mux.HandleFunc("GET /v1/tenants", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		// probeco stands in for a tenant whose issuer mapping no token can
		// resolve through: it exists, it lists, and nobody can ever sign in to
		// it. The listing has to say so, or the only way to find out is a
		// sign-in that lands somewhere else.
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"tenantId": "tenant-1", "name": "acme", "type": "COMPANY", "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z", "resolvable": true},
			{"tenantId": "tenant-3", "name": "probeco", "type": "COMPANY", "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z", "resolvable": false},
		})
	})
	mux.HandleFunc("PATCH /v1/tenant-issuers", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		if body["orgFieldKey"] != "" {
			orgScopedIssuers[body["issuer"]] = body["orgFieldKey"]
		}
		mu.Unlock()
		tenantID := body["tenantId"]
		if tenantID == "" {
			tenantID = "tenant-1"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tenantId": tenantID, "issuer": body["issuer"], "name": "repaired",
			"orgFieldKey": body["orgFieldKey"], "orgFieldValue": body["orgFieldValue"],
		})
	})
}

func registerPlatformEnvironmentStubRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/environments", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"environmentId": "env-1", "tenantId": "tenant-1", "name": "prod", "type": "runtime", "status": "running", "runtimeVersion": "1.2.3", "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z"},
		})
	})
	mux.HandleFunc("POST /v1/environments", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"environmentId": "env-2", "tenantId": "tenant-1", "name": body["name"], "type": body["type"], "status": "registered", "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z",
		})
	})
	mux.HandleFunc("GET /v1/environments/{environment_id}", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		if r.PathValue("environment_id") == "missing" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"environmentId": r.PathValue("environment_id"), "tenantId": "tenant-1", "name": "prod", "type": "runtime", "status": "running", "runtimeVersion": "1.2.3", "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z",
		})
	})
	mux.HandleFunc("POST /v1/environments/{environment_id}/deploy", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"environmentId": r.PathValue("environment_id"), "tenantId": "tenant-1", "name": "prod", "type": "runtime", "status": "provisioning", "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z",
		})
	})
	mux.HandleFunc("POST /v1/environments/{environment_id}/stop", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"environmentId": r.PathValue("environment_id"), "tenantId": "tenant-1", "name": "prod", "type": "runtime", "status": "running", "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z",
		})
	})
	mux.HandleFunc("DELETE /v1/environments/{environment_id}", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"environmentId": r.PathValue("environment_id"), "tenantId": "tenant-1", "name": "prod", "type": "runtime", "status": "deleting", "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z",
		})
	})
}

func registerPlatformDefinitionStubRoutes(mux *http.ServeMux) {
	// The definition store. Both handlers authenticate, so a scenario
	// proves the CLI actually attached the bearer it minted. The stored payload
	// is passed through verbatim and the revision advances on every upload,
	// exactly as environment_definitions' upsert does.
	var definitionMu sync.Mutex
	type storedDefinition struct {
		revision   int
		definition map[string]any
	}
	definitions := map[string]storedDefinition{}
	mux.HandleFunc("PUT /v1/environments/{environment_id}/definition", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		var body struct {
			Definition map[string]any `json:"definition"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		definitionMu.Lock()
		revision := definitions[r.PathValue("environment_id")].revision + 1
		definitions[r.PathValue("environment_id")] = storedDefinition{revision: revision, definition: body.Definition}
		definitionMu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"environmentId": r.PathValue("environment_id"), "tenantId": "tenant-1",
			"revision": revision, "definition": body.Definition, "writtenByUserId": "user-1",
			"createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z",
		})
	})
	mux.HandleFunc("GET /v1/environments/{environment_id}/definition", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		definitionMu.Lock()
		stored, ok := definitions[r.PathValue("environment_id")]
		definitionMu.Unlock()
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"environmentId": r.PathValue("environment_id"), "tenantId": "tenant-1",
			"revision": stored.revision, "definition": stored.definition, "writtenByUserId": "user-1",
			"createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z",
		})
	})
}

func registerPlatformContextStubRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/contexts", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"contextId": "ctx-1", "tenantId": "tenant-1", "name": "prod-cluster", "provider": "aws", "status": "running", "region": "eu-west-2", "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z"},
		})
	})
	mux.HandleFunc("GET /v1/contexts/{context_id}", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"contextId": r.PathValue("context_id"), "tenantId": "tenant-1", "name": "prod-cluster", "provider": "aws", "status": "running", "region": "eu-west-2", "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z",
		})
	})
	mux.HandleFunc("POST /v1/contexts", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if preview, _ := body["preview"].(bool); preview {
			_ = json.NewEncoder(w).Encode(map[string]any{"plan": []string{"context: bootstrap cluster prod via alias aws-main in eu-west-2"}})
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"plan": []string{"context: bootstrap cluster prod via alias aws-main in eu-west-2"},
			"context": map[string]any{
				"contextId": "ctx-2", "tenantId": "tenant-1", "name": body["name"], "provider": "aws", "status": "provisioning", "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z",
			},
		})
	})
	mux.HandleFunc("POST /v1/provision", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"plan": []string{
				"provision: tenant acme (resolved from token)",
				"quota: tenant has 1 of 10 environments — within quota",
				"namespace: would create acme-prod",
			},
			"quotaOk": true,
		})
	})
}
