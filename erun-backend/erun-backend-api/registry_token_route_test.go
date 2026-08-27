package backendapi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/mcptoken"
)

// TestHostedRegistryTokenRouteRegistersWithDefaultResolvers is the red-then-green
// case for the hosted registry being unreachable in production (#1494). It builds
// the handler the way a real deployment does — a database and an MCP signing key,
// and no injected resolvers at all — and asserts GET /v2/token is served.
//
// RED: defaultTo filled only the combined identity resolver and left tenant nil,
// so the registration gate in NewHandler never fired. The token service the whole
// hosted-registry feature depends on was absent from every deployment, and a push
// to registry.erunpaas.com authenticated against a route that did not exist.
func TestHostedRegistryTokenRouteRegistersWithDefaultResolvers(t *testing.T) {
	handler, err := NewHandler(HandlerOptions{
		DB: undialedDatabase(t),
		TokenVerifier: TokenVerifierFunc(func(context.Context, string) (Claims, error) {
			return Claims{Issuer: "https://issuer.example", Subject: "user-1"}, nil
		}),
		MCPSigner: testRegistrySigner(t),
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v2/token?service=registry.erunpaas.com\u0026scope=repository:acme/app:pull", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatal("GET /v2/token is not registered: the hosted registry token service does not wire up when a deployment injects no resolvers of its own")
	}
}

func testRegistrySigner(t *testing.T) *mcptoken.Signer {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatalf("marshal signing key: %v", err)
	}
	signer, err := mcptoken.NewSigner(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	if err != nil {
		t.Fatalf("build signer: %v", err)
	}
	return signer
}
