package dns01broker

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
)

// tokenVerifier verifies a per-env DNS-01 token and returns the (tenant,
// environment) it authorizes. Satisfied by *mcptoken.Signer.
type tokenVerifier interface {
	VerifyDNS01(token string, now time.Time) (tenant, environment string, err error)
}

// recordWriter applies the authorized challenge write. Satisfied by
// *PowerDNSWriter.
type recordWriter interface {
	Present(fqdn, value string) error
	CleanUp(fqdn, value string) error
}

// AuditSink records every authorized broker write. Best-effort: a write is not
// failed if auditing fails, but every write must be offered to the sink.
type AuditSink interface {
	RecordDNS01(ctx context.Context, tenant, environment, action, fqdn string)
}

// logAuditSink is the default sink: a structured log line per write. A
// DB-backed sink can replace it without touching the broker.
type logAuditSink struct{}

func (logAuditSink) RecordDNS01(_ context.Context, tenant, environment, action, fqdn string) {
	log.Printf("dns01 broker: tenant=%s env=%s action=%s fqdn=%s", tenant, environment, action, fqdn)
}

// Broker serves the authenticated DNS-01 present/cleanup endpoints. Each request
// carries a per-env token; the broker verifies it, authorizes the challenge FQDN
// against that env's subzone, and only then writes via the centrally-held TSIG
// credential. This is what makes per-env issuance safe on a shared cluster.
type Broker struct {
	verifier tokenVerifier
	writer   recordWriter
	zone     string
	audit    AuditSink
	now      func() time.Time
}

func NewBroker(verifier tokenVerifier, writer recordWriter, servicesZone string, audit AuditSink) *Broker {
	if audit == nil {
		audit = logAuditSink{}
	}
	return &Broker{verifier: verifier, writer: writer, zone: servicesZone, audit: audit, now: time.Now}
}

// challengeRequest is the broker's own contract; the per-cluster webhook shim
// marshals cert-manager's ChallengeRequest into it and carries the env token in
// the Authorization header.
type challengeRequest struct {
	FQDN  string `json:"fqdn"`
	Value string `json:"value"`
}

func (b *Broker) Register(mux *http.ServeMux) {
	mux.Handle("POST /v1/dns01/present", http.HandlerFunc(b.handlePresent))
	mux.Handle("POST /v1/dns01/cleanup", http.HandlerFunc(b.handleCleanup))
}

func (b *Broker) handlePresent(w http.ResponseWriter, r *http.Request) {
	b.serve(w, r, "present", func(fqdn, value string) error { return b.writer.Present(fqdn, value) })
}

func (b *Broker) handleCleanup(w http.ResponseWriter, r *http.Request) {
	b.serve(w, r, "cleanup", func(fqdn, value string) error { return b.writer.CleanUp(fqdn, value) })
}

func (b *Broker) serve(w http.ResponseWriter, r *http.Request, action string, apply func(fqdn, value string) error) {
	tenant, environment, ok := b.authenticate(w, r)
	if !ok {
		return
	}
	var body challengeRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.FQDN) == "" {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := AuthorizeChallenge(tenant, environment, b.zone, body.FQDN); err != nil {
		// 403, not 404: the caller is authenticated but not authorized for this
		// name. The message names the caller's own scope, never another tenant's.
		http.Error(w, "challenge fqdn is outside this environment's subzone", http.StatusForbidden)
		return
	}
	if err := apply(body.FQDN, body.Value); err != nil {
		http.Error(w, "dns update failed", http.StatusBadGateway)
		return
	}
	b.audit.RecordDNS01(r.Context(), tenant, environment, action, body.FQDN)
	w.WriteHeader(http.StatusNoContent)
}

// authenticate verifies the bearer DNS-01 token and returns the (tenant,
// environment) it authorizes, writing a 401 and returning ok=false otherwise.
func (b *Broker) authenticate(w http.ResponseWriter, r *http.Request) (tenant, environment string, ok bool) {
	token := bearerToken(r)
	if token == "" {
		http.Error(w, "missing bearer token", http.StatusUnauthorized)
		return "", "", false
	}
	tenant, environment, err := b.verifier.VerifyDNS01(token, b.now())
	if err != nil {
		http.Error(w, "invalid dns01 token", http.StatusUnauthorized)
		return "", "", false
	}
	return tenant, environment, true
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	rest, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(rest)
}
