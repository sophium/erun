package dns01broker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeVerifier struct {
	tenant, environment string
	err                 error
}

func (f fakeVerifier) VerifyDNS01(string, time.Time) (string, string, error) {
	return f.tenant, f.environment, f.err
}

type fakeWriter struct {
	presented []string
	cleaned   []string
	err       error
}

func (f *fakeWriter) Present(fqdn, value string) error {
	if f.err != nil {
		return f.err
	}
	f.presented = append(f.presented, fqdn+"="+value)
	return nil
}

func (f *fakeWriter) CleanUp(fqdn, value string) error {
	if f.err != nil {
		return f.err
	}
	f.cleaned = append(f.cleaned, fqdn+"="+value)
	return nil
}

type fakeAudit struct{ calls []string }

func (f *fakeAudit) RecordDNS01(_ context.Context, tenant, environment, action, fqdn string) {
	f.calls = append(f.calls, strings.Join([]string{tenant, environment, action, fqdn}, "|"))
}

func present(b *Broker, auth, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/dns01/present", strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	b.handlePresent(rec, req)
	return rec
}

const zone = "services.example.com"

func TestBrokerPresentAuthorizedWrite(t *testing.T) {
	writer := &fakeWriter{}
	audit := &fakeAudit{}
	b := NewBroker(fakeVerifier{tenant: "acme", environment: "prod"}, writer, zone, audit)
	rec := present(b, "Bearer good", `{"fqdn":"_acme-challenge.acme-prod.services.example.com","value":"tok"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if len(writer.presented) != 1 {
		t.Fatalf("writer.presented = %v, want one write", writer.presented)
	}
	if len(audit.calls) != 1 || audit.calls[0] != "acme|prod|present|_acme-challenge.acme-prod.services.example.com" {
		t.Fatalf("audit = %v", audit.calls)
	}
}

func TestBrokerRejectsMissingToken(t *testing.T) {
	writer := &fakeWriter{}
	b := NewBroker(fakeVerifier{tenant: "acme", environment: "prod"}, writer, zone, &fakeAudit{})
	rec := present(b, "", `{"fqdn":"_acme-challenge.acme-prod.services.example.com","value":"tok"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(writer.presented) != 0 {
		t.Fatal("no write may happen without a token")
	}
}

func TestBrokerRejectsInvalidToken(t *testing.T) {
	writer := &fakeWriter{}
	b := NewBroker(fakeVerifier{err: fmt.Errorf("bad")}, writer, zone, &fakeAudit{})
	rec := present(b, "Bearer nope", `{"fqdn":"_acme-challenge.acme-prod.services.example.com","value":"tok"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(writer.presented) != 0 {
		t.Fatal("no write on an invalid token")
	}
}

// TestBrokerRejectsCrossTenant is the impersonation guard end to end: a valid
// token for tenant acme cannot write another tenant's challenge — 403, no write.
func TestBrokerRejectsCrossTenant(t *testing.T) {
	writer := &fakeWriter{}
	audit := &fakeAudit{}
	b := NewBroker(fakeVerifier{tenant: "acme", environment: "prod"}, writer, zone, audit)
	rec := present(b, "Bearer good", `{"fqdn":"_acme-challenge.beta-prod.services.example.com","value":"tok"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if len(writer.presented) != 0 {
		t.Fatal("a cross-tenant challenge must not be written")
	}
	if len(audit.calls) != 0 {
		t.Fatal("a rejected challenge must not be audited as a write")
	}
}

func TestBrokerRejectsBadBody(t *testing.T) {
	b := NewBroker(fakeVerifier{tenant: "acme", environment: "prod"}, &fakeWriter{}, zone, &fakeAudit{})
	rec := present(b, "Bearer good", `not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestBrokerSurfacesWriteFailure(t *testing.T) {
	b := NewBroker(fakeVerifier{tenant: "acme", environment: "prod"}, &fakeWriter{err: fmt.Errorf("dns down")}, zone, &fakeAudit{})
	rec := present(b, "Bearer good", `{"fqdn":"_acme-challenge.acme-prod.services.example.com","value":"tok"}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}
