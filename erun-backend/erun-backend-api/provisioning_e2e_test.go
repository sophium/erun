package backendapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/secrets"
)

// TestProvisionContextEndToEnd is an opt-in end-to-end gate, like the k3d e2e
// test: it runs the real provisioning workflow against live infra (not mocks).
const e2eDevToken = "DEV-TOKEN"

func TestProvisionContextEndToEnd(t *testing.T) {
	if os.Getenv("ERUN_E2E_PROVISION") != "1" {
		t.Skip("opt-in: set ERUN_E2E_PROVISION=1 (+ a migrated Postgres, DBOS_SYSTEM_DATABASE_URL, floci on :4566)")
	}
	databaseURL := os.Getenv("ERUN_E2E_PROVISION_DATABASE_URL")
	dbosURL := os.Getenv("DBOS_SYSTEM_DATABASE_URL")
	if databaseURL == "" || dbosURL == "" {
		t.Skip("ERUN_E2E_PROVISION_DATABASE_URL and DBOS_SYSTEM_DATABASE_URL are required")
	}

	key, err := secrets.GenerateKey()
	mustNoErr(t, err, "generate key")
	cipher, err := secrets.NewCipher(key)
	mustNoErr(t, err, "new cipher")
	dbosCtx, err := dbos.NewDBOSContext(context.Background(), dbos.Config{AppName: "erun-provision-e2e", DatabaseURL: dbosURL})
	mustNoErr(t, err, "dbos context")
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	handler, err := NewHandler(HandlerOptions{
		TokenVerifier: TokenVerifierFunc(func(_ context.Context, token string) (Claims, error) {
			if token != e2eDevToken {
				return Claims{}, errors.New("invalid dev token")
			}
			return Claims{Issuer: "https://dev.local", Subject: "dev-user", Username: "dev"}, nil
		}),
		IdentityCache: NewIdentityResolutionCache(IdentityCacheOptions{}),
		DB:            db,
		DBDialect:     repository.DialectPostgres,
		DBOSContext:   dbosCtx,
		Cipher:        cipher,
		AWSEndpoint:   "http://localhost:4566",
	})
	mustNoErr(t, err, "new handler")
	mustNoErr(t, dbos.Launch(dbosCtx), "dbos launch")
	t.Cleanup(func() { dbos.Shutdown(dbosCtx, 5*time.Second) })

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	if code, body := e2eRequest(t, srv.URL, http.MethodPut, "/v1/cloud-provider-aliases/floci-aws",
		map[string]any{"provider": "aws", "credentials": `{"accessKeyId":"test","secretAccessKey":"test"}`}); code != http.StatusNoContent {
		t.Fatalf("set alias: HTTP %d: %s", code, body)
	}

	code, body := e2eRequest(t, srv.URL, http.MethodPost, "/v1/contexts",
		map[string]any{"name": "primary", "cloudProviderAlias": "floci-aws", "region": "eu-west-2", "preview": false})
	if code != http.StatusAccepted {
		t.Fatalf("create context: HTTP %d (want 202): %s", code, body)
	}
	var created struct {
		Context struct {
			ContextID string `json:"contextId"`
			Status    string `json:"status"`
		} `json:"context"`
	}
	mustNoErr(t, json.Unmarshal([]byte(body), &created), "parse create response")
	if created.Context.Status != "provisioning" {
		t.Fatalf("created status = %q, want provisioning", created.Context.Status)
	}

	awaitContextRunning(t, srv.URL, created.Context.ContextID)
}

// awaitContextRunning polls the context until the durable workflow reports a
// terminal state, failing on a failed provision or on timeout.
func awaitContextRunning(t *testing.T, baseURL, contextID string) {
	t.Helper()
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		c, b := e2eRequest(t, baseURL, http.MethodGet, "/v1/contexts/"+contextID, nil)
		if c != http.StatusOK {
			t.Fatalf("get context: HTTP %d: %s", c, b)
		}
		var got struct {
			Status         string `json:"status"`
			InstanceID     string `json:"instanceId"`
			PublicIP       string `json:"publicIp"`
			ProvisionError string `json:"provisionError"`
		}
		mustNoErr(t, json.Unmarshal([]byte(b), &got), "parse get response")
		switch got.Status {
		case "running":
			if got.InstanceID == "" || got.PublicIP == "" {
				t.Fatalf("running but missing instance/ip: %s", b)
			}
			return
		case "failed":
			t.Fatalf("provisioning failed: %s", got.ProvisionError)
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatal("timed out waiting for provisioning to reach running")
}

func e2eRequest(t *testing.T, baseURL, method, path string, body any) (int, string) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		mustNoErr(t, err, "marshal body")
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, baseURL+path, reader)
	mustNoErr(t, err, "new request")
	req.Header.Set("Authorization", "Bearer "+e2eDevToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	mustNoErr(t, err, "do request")
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	mustNoErr(t, err, "read body")
	return resp.StatusCode, string(out)
}

func mustNoErr(t *testing.T, err error, msg string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", msg, err)
	}
}
