package expose

import (
	"reflect"
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/orchestration/clusterread"
)

// cert-manager writes the tls block before issuance finishes, so scheme alone
// cannot say the certificate is valid yet.
func TestFilterExposedServicesReportsPendingCertificateHonestly(t *testing.T) {
	ingresses := []model.ObservedIngress{
		{
			Name:  "expose-web",
			Hosts: []string{"web.frs-prod.services.test"},
			TLS:   []model.ObservedIngressTLS{{Hosts: []string{"web.frs-prod.services.test"}, SecretName: "frs-prod-wildcard-tls"}},
		},
	}
	certs := []model.ObservedCertificate{{
		Name: "wildcard", Ready: false, Reason: "Issuing", Message: "waiting for order to complete",
		SecretName: "frs-prod-wildcard-tls",
	}}

	got := clusterread.FilterExposedServices(ingresses, certs)

	want := []clusterread.ExposedService{{
		Service: "web", Hostname: "web.frs-prod.services.test", Scheme: "https",
		TLSReady: false, TLSNotReadyReason: "Issuing: waiting for order to complete",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterExposedServices() = %+v, want %+v", got, want)
	}
}

func TestFilterExposedServicesWalksChallengeChainForTheRealBlocker(t *testing.T) {
	ingresses := []model.ObservedIngress{
		{
			Name:  "expose-web",
			Hosts: []string{"web.frs-prod.services.test"},
			TLS:   []model.ObservedIngressTLS{{Hosts: []string{"web.frs-prod.services.test"}, SecretName: "frs-prod-wildcard-tls"}},
		},
	}
	certs := []model.ObservedCertificate{{
		Name: "wildcard", Ready: false, Reason: "Issuing", SecretName: "frs-prod-wildcard-tls",
		Orders: []model.ObservedCertificateOrder{{
			Name: "wildcard-order-1", Reason: "Pending",
			Challenges: []model.ObservedCertificateChallenge{{
				Name: "wildcard-chal-1", Reason: "presenting DNS-01 challenge: RBAC denied for webhook solver",
			}},
		}},
	}}

	got := clusterread.FilterExposedServices(ingresses, certs)

	if len(got) != 1 || got[0].TLSNotReadyReason != "presenting DNS-01 challenge: RBAC denied for webhook solver" {
		t.Fatalf("filterExposedServices() = %+v, want the Challenge's own reason", got)
	}
}

func TestFilterExposedServicesReportsMissingCertificateHonestly(t *testing.T) {
	ingresses := []model.ObservedIngress{
		{
			Name:  "expose-web",
			Hosts: []string{"web.frs-prod.services.test"},
			TLS:   []model.ObservedIngressTLS{{Hosts: []string{"web.frs-prod.services.test"}, SecretName: "frs-prod-wildcard-tls"}},
		},
	}

	got := clusterread.FilterExposedServices(ingresses, nil)

	if len(got) != 1 || got[0].TLSReady || got[0].TLSNotReadyReason == "" {
		t.Fatalf("filterExposedServices() = %+v, want TLSReady false with a named reason", got)
	}
}
