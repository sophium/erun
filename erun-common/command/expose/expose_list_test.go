package expose

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/observe"
	"github.com/sophium/erun/erun-common/orchestration/clusterread"
)

// installReadNamingKubectl stubs kubectl so every read is empty except
// resource, which fails like an unreachable API server.
func installReadNamingKubectl(t *testing.T, resource string) {
	t.Helper()
	script := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"  *\"get " + resource + "\"*) echo 'the server could not find the requested resource' >&2; exit 1 ;;\n" +
		"  *) printf '%s' '{\"items\":[]}' ;;\n" +
		"esac\n"
	path := filepath.Join(t.TempDir(), "kubectl")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write kubectl stub: %v", err)
	}
	t.Setenv("ERUN_KUBECTL_BIN", path)
}

// The shared fetcher drops the "observe:" prefix only because observe re-adds
// it; both halves must change together.
func TestSharedReadsNameTheReadAndTheOperationNamesItself(t *testing.T) {
	installReadNamingKubectl(t, "ingress")
	req := config.ShellLaunchParams{Tenant: "frs", Environment: "dev", Namespace: "frs-dev"}

	_, err := clusterread.ListExposedServices(req)
	if err == nil {
		t.Fatal("expected the failing ingress read to surface")
	}
	if !strings.Contains(err.Error(), "get ingress:") {
		t.Fatalf("the read must name itself, got: %v", err)
	}
	if strings.Contains(err.Error(), "observe:") {
		t.Fatalf("the shared read names an operation %q's caller never ran: %v", "ListExposedServices", err)
	}

	_, err = observe.RunObservation(core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, io.Discard, io.Discard)}, req, model.ObserveParams{})
	if err == nil {
		t.Fatal("expected the failing ingress read to surface through observe")
	}
	if !strings.Contains(err.Error(), "observe: get ingress:") {
		t.Fatalf("observe must still name itself and the read it made, got: %v", err)
	}
}

func TestFilterExposedServicesKeepsOnlyExposeIngresses(t *testing.T) {
	ingresses := []model.ObservedIngress{
		{Name: "expose-api", Hosts: []string{"api.frs-prod.services.test"}},
		{Name: "some-other-ingress", Hosts: []string{"other.frs-prod.services.test"}},
		{
			Name:  "expose-web",
			Hosts: []string{"web.frs-prod.services.test"},
			TLS:   []model.ObservedIngressTLS{{Hosts: []string{"web.frs-prod.services.test"}, SecretName: "frs-prod-wildcard-tls"}},
		},
		{Name: "expose-broken", Hosts: nil},
	}
	certs := []model.ObservedCertificate{{Name: "wildcard", Ready: true, SecretName: "frs-prod-wildcard-tls"}}

	got := clusterread.FilterExposedServices(ingresses, certs)

	want := []clusterread.ExposedService{
		{Service: "api", Hostname: "api.frs-prod.services.test", Scheme: "http"},
		{Service: "web", Hostname: "web.frs-prod.services.test", Scheme: "https", TLSReady: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterExposedServices() = %+v, want %+v", got, want)
	}
}

func TestFilterExposedServicesEmptyWhenNoneMatch(t *testing.T) {
	got := clusterread.FilterExposedServices([]model.ObservedIngress{{Name: "some-other-ingress", Hosts: []string{"x"}}}, nil)
	if len(got) != 0 {
		t.Fatalf("expected no exposed services, got %+v", got)
	}
}

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
