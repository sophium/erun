package eruncommon

import (
	"reflect"
	"testing"
)

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

	got := filterExposedServices(ingresses)

	want := []ExposedService{
		{Service: "api", Hostname: "api.frs-prod.services.test", Scheme: "http"},
		{Service: "web", Hostname: "web.frs-prod.services.test", Scheme: "https"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterExposedServices() = %+v, want %+v", got, want)
	}
}

func TestFilterExposedServicesEmptyWhenNoneMatch(t *testing.T) {
	got := filterExposedServices([]ObservedIngress{{Name: "some-other-ingress", Hosts: []string{"x"}}})
	if len(got) != 0 {
		t.Fatalf("expected no exposed services, got %+v", got)
	}
}
