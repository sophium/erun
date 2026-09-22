package eruncommon

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// installReadNamingKubectl points ERUN_KUBECTL_BIN at a stub that answers every
// read with an empty list except resource, which fails the way an unreachable
// API server does. It is what lets a test drive the shared fetcher's own
// failure text without a cluster.
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

// TestSharedReadsNameTheReadAndTheOperationNamesItself pins the naming the
// shared kubectl fetchers now carry. `get ingress` is reached by erun e2e's
// exposure listing, the services listing, and observe alike, so a fetcher
// leading with "observe:" named a command two of its three callers never ran
// (root AGENTS.md's diagnostic record: a message names the command actually
// invoked, or names none).
//
// Both halves are load-bearing and neither alone is the contract: stripping
// the prefix from the fetcher is only correct because observe re-adds it, so a
// change that does either one without the other breaks the other caller's
// output -- the listing naming an operation it never ran, or observe losing
// the name its own output has always carried.
func TestSharedReadsNameTheReadAndTheOperationNamesItself(t *testing.T) {
	installReadNamingKubectl(t, "ingress")
	req := ShellLaunchParams{Tenant: "frs", Environment: "dev", Namespace: "frs-dev"}

	_, err := ListExposedServices(req)
	if err == nil {
		t.Fatal("expected the failing ingress read to surface")
	}
	if !strings.Contains(err.Error(), "get ingress:") {
		t.Fatalf("the read must name itself, got: %v", err)
	}
	if strings.Contains(err.Error(), "observe:") {
		t.Fatalf("the shared read names an operation %q's caller never ran: %v", "ListExposedServices", err)
	}

	_, err = RunObservation(Context{Logger: NewLoggerWithWriters(VerbosityInfo, io.Discard, io.Discard)}, req, ObserveParams{})
	if err == nil {
		t.Fatal("expected the failing ingress read to surface through observe")
	}
	if !strings.Contains(err.Error(), "observe: get ingress:") {
		t.Fatalf("observe must still name itself and the read it made, got: %v", err)
	}
}

// TestFilterExposedServicesKeepsOnlyExposeIngresses pins the baseline shape
// with an already-Ready certificate: nothing here changes for an env whose
// TLS has already issued, the case every exposed service eventually reaches.
func TestFilterExposedServicesKeepsOnlyExposeIngresses(t *testing.T) {
	ingresses := []ObservedIngress{
		{Name: "expose-api", Hosts: []string{"api.frs-prod.services.test"}},
		{Name: "some-other-ingress", Hosts: []string{"other.frs-prod.services.test"}},
		{
			Name:  "expose-web",
			Hosts: []string{"web.frs-prod.services.test"},
			TLS:   []ObservedIngressTLS{{Hosts: []string{"web.frs-prod.services.test"}, SecretName: "frs-prod-wildcard-tls"}},
		},
		// No host at all -- nothing to route or display.
		{Name: "expose-broken", Hosts: nil},
	}
	certs := []ObservedCertificate{{Name: "wildcard", Ready: true, SecretName: "frs-prod-wildcard-tls"}}

	got := filterExposedServices(ingresses, certs)

	want := []ExposedService{
		{Service: "api", Hostname: "api.frs-prod.services.test", Scheme: "http"},
		{Service: "web", Hostname: "web.frs-prod.services.test", Scheme: "https", TLSReady: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterExposedServices() = %+v, want %+v", got, want)
	}
}

func TestFilterExposedServicesEmptyWhenNoneMatch(t *testing.T) {
	got := filterExposedServices([]ObservedIngress{{Name: "some-other-ingress", Hosts: []string{"x"}}}, nil)
	if len(got) != 0 {
		t.Fatalf("expected no exposed services, got %+v", got)
	}
}

// TestFilterExposedServicesReportsPendingCertificateHonestly is the whole
// point of this addition: cert-manager writes the Ingress's tls block (and
// the Secret it names) before issuance finishes, so scheme alone cannot tell
// a caller that opening this URL right now would hit a certificate warning,
// not a valid cert. TLSNotReadyReason surfaces the Certificate's own
// condition here since the chain walk found nothing more specific.
func TestFilterExposedServicesReportsPendingCertificateHonestly(t *testing.T) {
	ingresses := []ObservedIngress{
		{
			Name:  "expose-web",
			Hosts: []string{"web.frs-prod.services.test"},
			TLS:   []ObservedIngressTLS{{Hosts: []string{"web.frs-prod.services.test"}, SecretName: "frs-prod-wildcard-tls"}},
		},
	}
	certs := []ObservedCertificate{{
		Name: "wildcard", Ready: false, Reason: "Issuing", Message: "waiting for order to complete",
		SecretName: "frs-prod-wildcard-tls",
	}}

	got := filterExposedServices(ingresses, certs)

	want := []ExposedService{{
		Service: "web", Hostname: "web.frs-prod.services.test", Scheme: "https",
		TLSReady: false, TLSNotReadyReason: "Issuing: waiting for order to complete",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterExposedServices() = %+v, want %+v", got, want)
	}
}

// TestFilterExposedServicesWalksChallengeChainForTheRealBlocker matches
// erun observe's own behavior: once the chain is walked, the Challenge's
// reason (e.g. a webhook solver's RBAC denial) is the one that actually
// explains a stuck issuance, not the Certificate's generic top-level reason.
func TestFilterExposedServicesWalksChallengeChainForTheRealBlocker(t *testing.T) {
	ingresses := []ObservedIngress{
		{
			Name:  "expose-web",
			Hosts: []string{"web.frs-prod.services.test"},
			TLS:   []ObservedIngressTLS{{Hosts: []string{"web.frs-prod.services.test"}, SecretName: "frs-prod-wildcard-tls"}},
		},
	}
	certs := []ObservedCertificate{{
		Name: "wildcard", Ready: false, Reason: "Issuing", SecretName: "frs-prod-wildcard-tls",
		Orders: []ObservedCertificateOrder{{
			Name: "wildcard-order-1", Reason: "Pending",
			Challenges: []ObservedCertificateChallenge{{
				Name: "wildcard-chal-1", Reason: "presenting DNS-01 challenge: RBAC denied for webhook solver",
			}},
		}},
	}}

	got := filterExposedServices(ingresses, certs)

	if len(got) != 1 || got[0].TLSNotReadyReason != "presenting DNS-01 challenge: RBAC denied for webhook solver" {
		t.Fatalf("filterExposedServices() = %+v, want the Challenge's own reason", got)
	}
}

// TestFilterExposedServicesReportsMissingCertificateHonestly: an Ingress can
// carry a tls block naming a Secret with no matching Certificate at all (a
// wildcard cert deleted out from under a still-live Ingress, for instance).
// Absence must read as "not ready", never as an assumed success.
func TestFilterExposedServicesReportsMissingCertificateHonestly(t *testing.T) {
	ingresses := []ObservedIngress{
		{
			Name:  "expose-web",
			Hosts: []string{"web.frs-prod.services.test"},
			TLS:   []ObservedIngressTLS{{Hosts: []string{"web.frs-prod.services.test"}, SecretName: "frs-prod-wildcard-tls"}},
		},
	}

	got := filterExposedServices(ingresses, nil)

	if len(got) != 1 || got[0].TLSReady || got[0].TLSNotReadyReason == "" {
		t.Fatalf("filterExposedServices() = %+v, want TLSReady false with a named reason", got)
	}
}
