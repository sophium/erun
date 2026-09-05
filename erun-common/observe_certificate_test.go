package eruncommon

import "testing"

func readyCertificate(name string) certificateItem {
	item := certificateItem{}
	item.Metadata.Name = name
	item.Status.Conditions = []certManagerCondition{{Type: "Ready", Status: "True"}}
	return item
}

func notReadyCertificate(name, reason, message string) certificateItem {
	item := certificateItem{}
	item.Metadata.Name = name
	item.Status.Conditions = []certManagerCondition{{Type: "Ready", Status: "False", Reason: reason, Message: message}}
	return item
}

func certificateRequestFor(name, certificateName, createdAt string) ownedResourceItem {
	item := ownedResourceItem{}
	item.Metadata.Name = name
	item.Metadata.CreationTimestamp = createdAt
	item.Metadata.Labels = map[string]string{"cert-manager.io/certificate-name": certificateName}
	return item
}

func orderOwnedBy(name, certificateRequestName, state, reason string) ownedResourceItem {
	item := ownedResourceItem{}
	item.Metadata.Name = name
	item.Metadata.OwnerReferences = []struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	}{{Kind: "CertificateRequest", Name: certificateRequestName}}
	item.Status.State = state
	item.Status.Reason = reason
	return item
}

func challengeOwnedBy(name, orderName, challengeType, dnsName, state, reason string) ownedResourceItem {
	item := ownedResourceItem{}
	item.Metadata.Name = name
	item.Metadata.OwnerReferences = []struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	}{{Kind: "Order", Name: orderName}}
	item.Spec.Type = challengeType
	item.Spec.DNSName = dnsName
	item.Status.State = state
	item.Status.Reason = reason
	return item
}

// TestBuildObservedCertificatesWalksToChallengeReason is the failure-chain
// contract this command exists for: a Certificate stuck on a webhook solver's
// RBAC denial reports that denial directly, without the caller composing the
// Certificate -> CertificateRequest -> Order -> Challenge walk itself.
func TestBuildObservedCertificatesWalksToChallengeReason(t *testing.T) {
	certs := []certificateItem{notReadyCertificate("wildcard", "Issuing", "Issuing certificate as Secret does not exist")}
	requests := []ownedResourceItem{certificateRequestFor("wildcard-abc123", "wildcard", "2024-01-01T00:00:00Z")}
	orders := []ownedResourceItem{orderOwnedBy("wildcard-order-1", "wildcard-abc123", "pending", "")}
	challenges := []ownedResourceItem{
		challengeOwnedBy("wildcard-challenge-1", "wildcard-order-1", "DNS-01", "example.services.test",
			"invalid", `unable to create webhook solver: RBAC denied: solvers.acme.cert-manager.io is forbidden: User "system:serviceaccount:default:cert-manager" cannot create resource "challenges" in API group "acme.cert-manager.io"`),
	}

	observed := buildObservedCertificates(certs, requests, orders, challenges)

	if len(observed) != 1 {
		t.Fatalf("expected 1 observed certificate, got %d", len(observed))
	}
	cert := observed[0]
	if cert.Ready {
		t.Fatalf("expected certificate to be reported not ready")
	}
	if len(cert.Orders) != 1 {
		t.Fatalf("expected 1 order in the walk, got %d", len(cert.Orders))
	}
	order := cert.Orders[0]
	if len(order.Challenges) != 1 {
		t.Fatalf("expected 1 challenge in the walk, got %d", len(order.Challenges))
	}
	challenge := order.Challenges[0]
	if challenge.Reason == "" {
		t.Fatalf("expected the challenge's RBAC-denial reason to surface, got empty string")
	}
	if got, want := challenge.Reason, challenges[0].Status.Reason; got != want {
		t.Fatalf("challenge reason = %q, want %q", got, want)
	}
}

func TestBuildObservedCertificatesReadyCertificateSkipsWalk(t *testing.T) {
	certs := []certificateItem{readyCertificate("wildcard")}
	// A ready certificate must never walk the chain, even when stale
	// CertificateRequest/Order/Challenge objects for it still exist.
	requests := []ownedResourceItem{certificateRequestFor("wildcard-old", "wildcard", "2024-01-01T00:00:00Z")}
	orders := []ownedResourceItem{orderOwnedBy("wildcard-order-old", "wildcard-old", "valid", "")}

	observed := buildObservedCertificates(certs, requests, orders, nil)

	if len(observed) != 1 {
		t.Fatalf("expected 1 observed certificate, got %d", len(observed))
	}
	if !observed[0].Ready {
		t.Fatalf("expected certificate to be reported ready")
	}
	if observed[0].Orders != nil {
		t.Fatalf("expected no orders for a ready certificate, got %+v", observed[0].Orders)
	}
}

// TestLatestCertificateRequestNamePicksMostRecent covers a Certificate that
// has been reissued: multiple CertificateRequests carry its label, and only
// the latest one's Order/Challenge chain is the live one worth walking.
func TestLatestCertificateRequestNamePicksMostRecent(t *testing.T) {
	requests := []ownedResourceItem{
		certificateRequestFor("wildcard-old", "wildcard", "2024-01-01T00:00:00Z"),
		certificateRequestFor("wildcard-new", "wildcard", "2024-06-01T00:00:00Z"),
		certificateRequestFor("other-cert-request", "other", "2025-01-01T00:00:00Z"),
	}

	name, ok := latestCertificateRequestName(requests, "wildcard")
	if !ok {
		t.Fatalf("expected a certificate request to be found")
	}
	if name != "wildcard-new" {
		t.Fatalf("latestCertificateRequestName = %q, want %q", name, "wildcard-new")
	}
}

func TestBuildObservedCertificatesNoMatchingRequestReportsNoOrders(t *testing.T) {
	certs := []certificateItem{notReadyCertificate("wildcard", "DoesNotExist", "")}

	observed := buildObservedCertificates(certs, nil, nil, nil)

	if len(observed) != 1 {
		t.Fatalf("expected 1 observed certificate, got %d", len(observed))
	}
	if observed[0].Orders != nil {
		t.Fatalf("expected no orders when no certificate request matches, got %+v", observed[0].Orders)
	}
}
