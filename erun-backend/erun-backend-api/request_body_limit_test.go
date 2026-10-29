package backendapi

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/zitadel"
)

// apiRequestBodyCap is the request body limit the API protocol documents.
const apiRequestBodyCap = 1 << 20

// unreachableDatabase grants no connection and counts every one asked for: the
// invite accepter's first step opens a transaction, so a request that reached it
// shows up here.
type unreachableDatabase struct {
	connects int
}

func (d *unreachableDatabase) Connect(context.Context) (driver.Conn, error) {
	d.connects++
	return nil, errors.New("the test database accepts no connections")
}

func (d *unreachableDatabase) Driver() driver.Driver {
	return unreachableDriver{database: d}
}

type unreachableDriver struct {
	database *unreachableDatabase
}

func (d unreachableDriver) Open(string) (driver.Conn, error) {
	return d.database.Connect(context.Background())
}

// countingBody records how much of the request body the handler read.
type countingBody struct {
	reader io.Reader
	read   int
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.read += n
	return n, err
}

// newInviteAcceptHandler builds the real handler with invite acceptance registered,
// over a database and an identity provider that record any use.
func newInviteAcceptHandler(t *testing.T) (http.Handler, *unreachableDatabase) {
	t.Helper()
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("identity provider called with %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(idp.Close)
	patPath := filepath.Join(t.TempDir(), "pat")
	if err := os.WriteFile(patPath, []byte("test-pat"), 0o600); err != nil {
		t.Fatalf("write pat: %v", err)
	}
	admin, err := zitadel.NewClientFromFile(zitadel.Config{BaseURL: idp.URL, ExternalDomain: "idp.example", PATPath: patPath})
	if err != nil || admin == nil {
		t.Fatalf("zitadel client: %v", err)
	}
	database := &unreachableDatabase{}
	db := sql.OpenDB(database)
	t.Cleanup(func() { _ = db.Close() })
	handler, err := NewHandler(HandlerOptions{
		TokenVerifier: TokenVerifierFunc(func(context.Context, string) (Claims, error) {
			return Claims{}, ErrInvalidBearerToken
		}),
		DB:            db,
		IdentityAdmin: admin,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	if database.connects != 0 {
		t.Fatalf("building the handler opened %d database connections", database.connects)
	}
	return handler, database
}

func serveInviteAccept(handler http.Handler, body *countingBody) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/invites/accept", body))
	return rec
}

// POST /v1/invites/accept is public and decodes its body before anything is
// trusted, so an unterminated token string must not be buffered without end.
func TestHandlerStopsReadingAnInviteAcceptBodyAtTheCap(t *testing.T) {
	handler, database := newInviteAcceptHandler(t)
	body := &countingBody{reader: strings.NewReader(`{"token":"` + strings.Repeat("a", 8*apiRequestBodyCap))}

	rec := serveInviteAccept(handler, body)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if database.connects != 0 {
		t.Errorf("an oversized body reached the invite accepter (%d database connections)", database.connects)
	}
	if body.read > apiRequestBodyCap+1<<12 {
		t.Errorf("read %d bytes of the body; reading must stop near the %d-byte cap", body.read, apiRequestBodyCap)
	}
}

// The cap is exact: a body that fills it still reaches the accepter, one byte more does not.
func TestHandlerAdmitsAnInviteAcceptBodyUpToTheCapAndNoFurther(t *testing.T) {
	accept := `{"token":"invite-token","username":"bob","password":"chosen"}`
	atCap := strings.Repeat(" ", apiRequestBodyCap-len(accept)) + accept

	handler, database := newInviteAcceptHandler(t)
	serveInviteAccept(handler, &countingBody{reader: strings.NewReader(atCap)})
	if database.connects == 0 {
		t.Fatalf("a %d-byte body, exactly the cap, did not reach the invite accepter", len(atCap))
	}

	handler, database = newInviteAcceptHandler(t)
	rec := serveInviteAccept(handler, &countingBody{reader: strings.NewReader(" " + atCap)})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d for a body one byte over the cap, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if database.connects != 0 {
		t.Errorf("a body one byte over the cap reached the invite accepter (%d database connections)", database.connects)
	}
}
