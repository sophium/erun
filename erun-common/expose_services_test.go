package eruncommon

import "testing"

// TestParseObservedServicesSortsAndKeepsPorts pins what the picker reads: a
// stable order (kubectl's own is not something a UI should inherit) and every
// port, since a multi-port Service is exactly the case where the caller has to
// choose one.
func TestParseObservedServicesSortsAndKeepsPorts(t *testing.T) {
	var list serviceList
	list.Items = append(list.Items, serviceItem{})
	list.Items[0].Metadata.Name = "web"
	list.Items[0].Spec.Type = "ClusterIP"
	list.Items = append(list.Items, serviceItem{})
	list.Items[1].Metadata.Name = "api"
	list.Items[1].Spec.Type = "ClusterIP"
	list.Items[1].Spec.Ports = append(list.Items[1].Spec.Ports, struct {
		Name     string `json:"name"`
		Port     int    `json:"port"`
		Protocol string `json:"protocol"`
	}{Name: "http", Port: 8000, Protocol: "TCP"}, struct {
		Name     string `json:"name"`
		Port     int    `json:"port"`
		Protocol string `json:"protocol"`
	}{Name: "metrics", Port: 9090, Protocol: "TCP"})

	services := parseObservedServices(list)
	if len(services) != 2 || services[0].Name != "api" || services[1].Name != "web" {
		t.Fatalf("services = %+v, want api before web", services)
	}
	if len(services[0].Ports) != 2 || services[0].Ports[0].Port != 8000 || services[0].Ports[1].Name != "metrics" {
		t.Fatalf("api ports = %+v, want both ports in order", services[0].Ports)
	}
}

// TestMergeServicesWithExposuresMatchesOnTheIngressBackend is the whole point
// of reading the backend: the public label and the Service name differ whenever
// the chart is the repo's own, and matching on the label would leave a service
// that IS exposed looking unexposed (and offer to expose it again).
func TestMergeServicesWithExposuresMatchesOnTheIngressBackend(t *testing.T) {
	services := []ObservedService{
		{Name: "validation-agent-backend-api", Ports: []ObservedServicePort{{Port: 8000}}},
		{Name: "frs-api", Ports: []ObservedServicePort{{Port: 80}}},
	}
	ingresses := []ObservedIngress{
		{
			Name:     "expose-validator",
			Hosts:    []string{"validator.frs-dev.services.example.com"},
			TLS:      []ObservedIngressTLS{{Hosts: []string{"validator.frs-dev.services.example.com"}, SecretName: "frs-dev-wildcard-tls"}},
			Backends: []ObservedIngressBackend{{Service: "validation-agent-backend-api", Port: 8000}},
		},
		// A non-expose Ingress the repo's own chart rendered must not be read as
		// an erun exposure.
		{Name: "validation-agent-backend-api", Hosts: []string{"validator.localtest.me"}},
	}

	merged := mergeServicesWithExposures(services, ingresses)
	if len(merged) != 2 {
		t.Fatalf("merged = %+v, want one entry per service", merged)
	}
	if merged[0].Exposure == nil {
		t.Fatal("expected the backend-matched service to carry its exposure")
	}
	if merged[0].Exposure.Label != "validator" || merged[0].Exposure.Scheme != "https" {
		t.Fatalf("exposure = %+v, want label validator over https", merged[0].Exposure)
	}
	if merged[1].Exposure != nil {
		t.Fatalf("frs-api is not exposed, got %+v", merged[1].Exposure)
	}
}

// An exposure whose Ingress carries no tls block for its host reads as http,
// never as the scheme someone asked for.
func TestMergeServicesWithExposuresReportsHTTPWithoutTLS(t *testing.T) {
	merged := mergeServicesWithExposures(
		[]ObservedService{{Name: "frs-api"}},
		[]ObservedIngress{{
			Name:     "expose-api",
			Hosts:    []string{"api.frs-dev.services.example.com"},
			Backends: []ObservedIngressBackend{{Service: "frs-api", Port: 80}},
		}},
	)
	if merged[0].Exposure == nil || merged[0].Exposure.Scheme != "http" {
		t.Fatalf("exposure = %+v, want http", merged[0].Exposure)
	}
}

// TestResolveExposeBackendServiceKeepsTheConvention locks the default: an
// existing caller that names no backend still gets <tenant>-<service>, so
// nothing that works today changes.
func TestResolveExposeBackendServiceKeepsTheConvention(t *testing.T) {
	if got := resolveExposeBackendService("", "frs", "api"); got != "frs-api" {
		t.Fatalf("derived backend = %q, want frs-api", got)
	}
	if got := resolveExposeBackendService("  validation-agent-backend-api ", "validationagent", "validator"); got != "validation-agent-backend-api" {
		t.Fatalf("explicit backend = %q, want it honored and trimmed", got)
	}
}
