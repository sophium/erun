package eruncommon

import (
	"errors"
	"fmt"
	"strings"
)

// ExposedService is one active exposure erun-expose created for an
// environment: a Host-routing Ingress named "expose-<service>", discovered by
// listing the env namespace's Ingresses. The Ingress is the source of truth,
// so there is no separate record to keep in sync with it.
type ExposedService struct {
	Service  string `json:"service"`
	Hostname string `json:"hostname"`
	Scheme   string `json:"scheme"`
	// BackendService is the in-namespace Service the Ingress routes to, read
	// from the Ingress rather than derived from Service: the two differ
	// whenever the chart that rendered the Service is the repo's own.
	BackendService string `json:"backendService,omitempty"`
	// TLSReady is meaningful only when Scheme is "https". cert-manager writes
	// the Ingress's tls block (and the Secret it names) before issuance
	// completes, so the block's mere presence -- what Scheme alone reflects --
	// cannot tell a caller whether opening the URL right now hits a valid
	// certificate or a warning. TLSReady is true only once the Certificate
	// backing that Secret has actually reached cert-manager's own Ready
	// condition.
	TLSReady bool `json:"tlsReady,omitempty"`
	// TLSNotReadyReason names why, resolved the same way `erun observe`
	// resolves it: cert-manager's own Certificate condition, or -- once the
	// chain is walked -- the deepest CertificateRequest/Order/Challenge reason
	// available. Empty whenever TLSReady is true or Scheme is "http".
	TLSNotReadyReason string `json:"tlsNotReadyReason,omitempty"`
}

// exposeIngressNamePrefix matches the name resolveExposeServicePlan gives
// every Ingress RunExposeService applies.
const exposeIngressNamePrefix = "expose-"

// ErrListExposedServicesForbidden reports that the caller's Kubernetes
// credentials are not authorized to list the env namespace's Ingresses or
// Certificates, so a caller can render a permission-restricted state rather
// than an empty one.
var ErrListExposedServicesForbidden = errors.New("list exposed services: forbidden")

// ListExposedServices reports the environment's active exposures by reading
// its namespace's Ingresses and Certificates and keeping the exposures
// erun-expose created among the former, each carrying the real TLS
// readiness of the certificate the latter backs it with -- never inferred
// from the Ingress carrying a tls block alone, which is true well before
// issuance actually completes.
func ListExposedServices(req ShellLaunchParams) ([]ExposedService, error) {
	ingresses, err := fetchObservedIngresses(observeGetArgs(req, "ingress"))
	if err != nil {
		if isForbiddenKubectlError(err) {
			return nil, fmt.Errorf("%w: %s", ErrListExposedServicesForbidden, err)
		}
		return nil, err
	}
	certs, err := fetchObservedCertificates(Context{}, req, observeGetArgs(req, "certificates.cert-manager.io"))
	if err != nil {
		if isForbiddenKubectlError(err) {
			return nil, fmt.Errorf("%w: %s", ErrListExposedServicesForbidden, err)
		}
		return nil, err
	}
	return filterExposedServices(ingresses, certs), nil
}

// filterExposedServices maps observed Ingresses to the exposures erun-expose
// created among them, split out from ListExposedServices as a pure function so
// it can be tested without a kubectl process.
func filterExposedServices(ingresses []ObservedIngress, certs []ObservedCertificate) []ExposedService {
	tlsSecretByHost := tlsSecretsByHost(ingresses)
	certBySecret := certificatesBySecret(certs)
	services := make([]ExposedService, 0, len(ingresses))
	for _, ing := range ingresses {
		service, ok := strings.CutPrefix(ing.Name, exposeIngressNamePrefix)
		if !ok || service == "" || len(ing.Hosts) == 0 {
			continue
		}
		host := ing.Hosts[0]
		exposed := ExposedService{Service: service, Hostname: host, Scheme: "http"}
		if secretName, hasTLS := tlsSecretByHost[host]; hasTLS {
			exposed.Scheme = "https"
			exposed.TLSReady, exposed.TLSNotReadyReason = tlsReadiness(secretName, certBySecret)
		}
		if len(ing.Backends) > 0 {
			exposed.BackendService = ing.Backends[0].Service
		}
		services = append(services, exposed)
	}
	return services
}

// tlsSecretsByHost maps each TLS-terminated host to the Secret name its
// Ingress's tls block names, across every observed Ingress.
func tlsSecretsByHost(ingresses []ObservedIngress) map[string]string {
	tlsSecretByHost := make(map[string]string)
	for _, ing := range ingresses {
		for _, tls := range ing.TLS {
			for _, host := range tls.Hosts {
				tlsSecretByHost[host] = tls.SecretName
			}
		}
	}
	return tlsSecretByHost
}

func certificatesBySecret(certs []ObservedCertificate) map[string]ObservedCertificate {
	certBySecret := make(map[string]ObservedCertificate, len(certs))
	for _, cert := range certs {
		if cert.SecretName != "" {
			certBySecret[cert.SecretName] = cert
		}
	}
	return certBySecret
}

// tlsReadiness resolves whether the Certificate backing secretName has
// actually reached Ready, and if not, why -- an absent Certificate (a
// wildcard cert deleted out from under a still-live Ingress, for instance)
// reads as not ready with a named reason, never as an assumed success.
func tlsReadiness(secretName string, certBySecret map[string]ObservedCertificate) (ready bool, notReadyReason string) {
	cert, found := certBySecret[secretName]
	if !found {
		return false, fmt.Sprintf("no certificate found for secret %s", secretName)
	}
	if !cert.Ready {
		return false, certificateNotReadyReason(cert)
	}
	return true, ""
}

// certificateNotReadyReason picks the most specific reason available for why
// a Certificate has not reached Ready, walking into the
// CertificateRequest -> Order -> Challenge chain `erun observe` already
// resolves (populated only when the Certificate itself is not Ready) for the
// blocker that actually explains a stuck issuance -- such as a webhook
// solver's RBAC denial -- rather than stopping at the Certificate's own,
// often generic, top-level condition.
func certificateNotReadyReason(cert ObservedCertificate) string {
	for _, order := range cert.Orders {
		for _, challenge := range order.Challenges {
			if challenge.Reason != "" {
				return challenge.Reason
			}
		}
		if order.Reason != "" {
			return order.Reason
		}
	}
	switch {
	case cert.Reason != "" && cert.Message != "":
		return cert.Reason + ": " + cert.Message
	case cert.Message != "":
		return cert.Message
	case cert.Reason != "":
		return cert.Reason
	default:
		return "certificate not yet issued"
	}
}
