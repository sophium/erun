package observe

import (
	"testing"

	"github.com/sophium/erun/erun-common/orchestration/clusterread"
)

func readyCertificate(name string) clusterread.CertificateItem {
	item := clusterread.CertificateItem{}
	item.Metadata.Name = name
	item.Status.Conditions = []clusterread.CertManagerCondition{{Type: "Ready", Status: "True"}}
	return item
}

func notReadyCertificate(name, reason, message string) clusterread.CertificateItem {
	item := clusterread.CertificateItem{}
	item.Metadata.Name = name
	item.Status.Conditions = []clusterread.CertManagerCondition{{Type: "Ready", Status: "False", Reason: reason, Message: message}}
	return item
}

func certificateRequestFor(name, certificateName, createdAt string) clusterread.OwnedResourceItem {
	item := clusterread.OwnedResourceItem{}
	item.Metadata.Name = name
	item.Metadata.CreationTimestamp = createdAt
	item.Metadata.Labels = map[string]string{"cert-manager.io/certificate-name": certificateName}
	return item
}

func orderOwnedBy(name, certificateRequestName, state, reason string) clusterread.OwnedResourceItem {
	item := clusterread.OwnedResourceItem{}
	item.Metadata.Name = name
	item.Metadata.OwnerReferences = []struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	}{{Kind: "CertificateRequest", Name: certificateRequestName}}
	item.Status.State = state
	item.Status.Reason = reason
	return item
}

func TestBuildObservedCertificatesReadyCertificateSkipsWalk(t *testing.T) {
	certs := []clusterread.CertificateItem{readyCertificate("wildcard")}
	// A ready certificate never walks the chain, even with stale requests present.
	requests := []clusterread.OwnedResourceItem{certificateRequestFor("wildcard-old", "wildcard", "2024-01-01T00:00:00Z")}
	orders := []clusterread.OwnedResourceItem{orderOwnedBy("wildcard-order-old", "wildcard-old", "valid", "")}

	observed := clusterread.BuildObservedCertificates(certs, requests, orders, nil)

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

func TestLatestCertificateRequestNamePicksMostRecent(t *testing.T) {
	requests := []clusterread.OwnedResourceItem{
		certificateRequestFor("wildcard-old", "wildcard", "2024-01-01T00:00:00Z"),
		certificateRequestFor("wildcard-new", "wildcard", "2024-06-01T00:00:00Z"),
		certificateRequestFor("other-cert-request", "other", "2025-01-01T00:00:00Z"),
	}

	name, ok := clusterread.LatestCertificateRequestName(requests, "wildcard")
	if !ok {
		t.Fatalf("expected a certificate request to be found")
	}
	if name != "wildcard-new" {
		t.Fatalf("latestCertificateRequestName = %q, want %q", name, "wildcard-new")
	}
}

func TestBuildObservedCertificatesNoMatchingRequestReportsNoOrders(t *testing.T) {
	certs := []clusterread.CertificateItem{notReadyCertificate("wildcard", "DoesNotExist", "")}

	observed := clusterread.BuildObservedCertificates(certs, nil, nil, nil)

	if len(observed) != 1 {
		t.Fatalf("expected 1 observed certificate, got %d", len(observed))
	}
	if observed[0].Orders != nil {
		t.Fatalf("expected no orders when no certificate request matches, got %+v", observed[0].Orders)
	}
}
