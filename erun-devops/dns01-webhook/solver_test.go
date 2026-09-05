package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cert-manager/cert-manager/pkg/acme/webhook/apis/acme/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	extapi "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func mustConfigJSON(t *testing.T, brokerURL string) *extapi.JSON {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"brokerURL":      brokerURL,
		"tokenSecretRef": map[string]string{"name": "acme-prod-dns01-token", "key": "token"},
	})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return &extapi.JSON{Raw: raw}
}

// TestBrokerSolverForwardsChallenge proves the shim reads the env's token from
// its Secret and forwards the challenge to the broker as an authenticated POST,
// hitting the action-specific path — the whole job of the shim.
func TestBrokerSolverForwardsChallenge(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "acme-prod-dns01-token", Namespace: "acme-prod"},
		Data:       map[string][]byte{"token": []byte("env-token-xyz")},
	}
	kube := fake.NewSimpleClientset(secret)

	type captured struct{ path, auth, body string }
	seen := make(chan captured, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen <- captured{path: r.URL.Path, auth: r.Header.Get("Authorization"), body: string(body)}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	solver := &brokerSolver{kube: kube, http: server.Client()}
	ch := &v1alpha1.ChallengeRequest{
		ResourceNamespace: "acme-prod",
		ResolvedFQDN:      "_acme-challenge.acme-prod.services.example.com.",
		Key:               "challenge-value",
		Config:            mustConfigJSON(t, server.URL+"/v1/dns01"),
	}

	if err := solver.Present(ch); err != nil {
		t.Fatalf("present: %v", err)
	}
	got := <-seen
	if got.path != "/v1/dns01/present" {
		t.Fatalf("present path = %q, want /v1/dns01/present", got.path)
	}
	if got.auth != "Bearer env-token-xyz" {
		t.Fatalf("auth = %q, want Bearer env-token-xyz", got.auth)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(got.body), &payload); err != nil {
		t.Fatalf("body: %v", err)
	}
	if payload["fqdn"] != ch.ResolvedFQDN || payload["value"] != ch.Key {
		t.Fatalf("payload = %v, want fqdn=%q value=%q", payload, ch.ResolvedFQDN, ch.Key)
	}

	if err := solver.CleanUp(ch); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if got := <-seen; got.path != "/v1/dns01/cleanup" {
		t.Fatalf("cleanup path = %q, want /v1/dns01/cleanup", got.path)
	}
}

// TestBrokerSolverSurfacesRejection proves a broker refusal (e.g. the 403 for a
// cross-tenant challenge) fails the challenge rather than reporting success.
func TestBrokerSolverSurfacesRejection(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "acme-prod-dns01-token", Namespace: "acme-prod"},
		Data:       map[string][]byte{"token": []byte("env-token-xyz")},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	solver := &brokerSolver{kube: fake.NewSimpleClientset(secret), http: server.Client()}
	ch := &v1alpha1.ChallengeRequest{
		ResourceNamespace: "acme-prod",
		ResolvedFQDN:      "_acme-challenge.acme-prod.services.example.com.",
		Key:               "v",
		Config:            mustConfigJSON(t, server.URL+"/v1/dns01"),
	}
	if err := solver.Present(ch); err == nil {
		t.Fatal("expected an error when the broker rejects the challenge")
	}
}

func TestLoadConfigRejectsIncomplete(t *testing.T) {
	if _, err := loadConfig(nil); err == nil {
		t.Fatal("expected an error for nil config")
	}
	raw, _ := json.Marshal(map[string]any{"brokerURL": "https://x"})
	if _, err := loadConfig(&extapi.JSON{Raw: raw}); err == nil {
		t.Fatal("expected an error when tokenSecretRef is missing")
	}
}

// TestCleanUpToleratesADeletedTokenSecret is the regression test for #1174.
// An environment delete removes the env's DNS-01 token Secret as ordinary
// namespace content, with no ordering guarantee against cert-manager
// finalizing a still-pending Challenge in the same namespace. If cleanup
// errors there it errors forever -- nothing recreates the Secret -- so the
// acme.cert-manager.io finalizer never clears and the namespace sits in
// Terminating for its full 20-minute timeout.
func TestCleanUpToleratesADeletedTokenSecret(t *testing.T) {
	// No Secret at all: exactly the state namespace teardown leaves behind.
	solver := &brokerSolver{kube: fake.NewSimpleClientset(), http: http.DefaultClient}
	ch := &v1alpha1.ChallengeRequest{
		ResourceNamespace: "acme-prod",
		ResolvedFQDN:      "_acme-challenge.acme-prod.services.example.com.",
		Key:               "challenge-value",
		Config:            mustConfigJSON(t, "http://broker.invalid/v1/dns01"),
	}

	if err := solver.CleanUp(ch); err != nil {
		t.Fatalf("cleanup with a deleted token secret = %v, want nil so the challenge finalizer can clear", err)
	}

	// Present must NOT tolerate it: a missing token when presenting is a real
	// misconfiguration, and swallowing it would hand back a certificate that
	// silently never gets a record.
	if err := solver.Present(ch); err == nil {
		t.Fatal("present with a missing token secret returned nil; a missing credential must surface when presenting")
	}
}

// TestCleanUpStillFailsWhenTheSecretExistsButTheBrokerIsUnreachable holds the
// tolerance to the one case it is for. A reachable-but-broken broker, or any
// other cleanup failure, must keep returning an error so cert-manager retries
// rather than dropping the record on the floor.
func TestCleanUpStillFailsWhenTheSecretExistsButTheBrokerIsUnreachable(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "acme-prod-dns01-token", Namespace: "acme-prod"},
		Data:       map[string][]byte{"token": []byte("env-token-xyz")},
	}
	solver := &brokerSolver{kube: fake.NewSimpleClientset(secret), http: http.DefaultClient}
	ch := &v1alpha1.ChallengeRequest{
		ResourceNamespace: "acme-prod",
		ResolvedFQDN:      "_acme-challenge.acme-prod.services.example.com.",
		Key:               "challenge-value",
		Config:            mustConfigJSON(t, "http://127.0.0.1:1/v1/dns01"),
	}

	if err := solver.CleanUp(ch); err == nil {
		t.Fatal("cleanup with a live token but an unreachable broker returned nil; that record is still in the zone")
	}
}

// TestCleanUpDoesNotTolerateASecretMissingItsKey: a Secret that exists but has
// no token key is a misconfiguration, not a deleted environment, so it must
// not take the tolerance path.
func TestCleanUpDoesNotTolerateASecretMissingItsKey(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "acme-prod-dns01-token", Namespace: "acme-prod"},
		Data:       map[string][]byte{"not-the-token": []byte("x")},
	}
	solver := &brokerSolver{kube: fake.NewSimpleClientset(secret), http: http.DefaultClient}
	ch := &v1alpha1.ChallengeRequest{
		ResourceNamespace: "acme-prod",
		ResolvedFQDN:      "_acme-challenge.acme-prod.services.example.com.",
		Key:               "challenge-value",
		Config:            mustConfigJSON(t, "http://broker.invalid/v1/dns01"),
	}

	if err := solver.CleanUp(ch); err == nil {
		t.Fatal("cleanup with a Secret missing its token key returned nil; that is a misconfiguration and must surface")
	}
}
