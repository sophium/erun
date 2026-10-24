package clusterread

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// ExposedService is one erun-expose Ingress ("expose-<service>"); the Ingress is the source of truth.
type ExposedService struct {
	Service  string `json:"service"`
	Hostname string `json:"hostname"`
	Scheme   string `json:"scheme"`
	// BackendService is read from the Ingress: a repo-native chart's Service name differs.
	BackendService string `json:"backendService,omitempty"`
	// TLSReady is true only once the backing Certificate is Ready; cert-manager writes
	// the tls block before issuance completes.
	TLSReady          bool   `json:"tlsReady,omitempty"`
	TLSNotReadyReason string `json:"tlsNotReadyReason,omitempty"`
}

const ExposeIngressNamePrefix = "expose-"

// ErrListExposedServicesForbidden reports that RBAC denies listing Ingresses or Certificates.
var ErrListExposedServicesForbidden = errors.New("list exposed services: forbidden")

// ListExposedServices reports the environment's erun-expose exposures with real TLS readiness.
func ListExposedServices(req config.ShellLaunchParams) ([]ExposedService, error) {
	ingresses, err := FetchObservedIngresses(ObserveGetArgs(req, "ingress"))
	if err != nil {
		if IsForbiddenKubectlError(err) {
			return nil, fmt.Errorf("%w: %w", ErrListExposedServicesForbidden, err)
		}
		return nil, err
	}
	certs, err := FetchObservedCertificates(core.Context{}, req, ObserveGetArgs(req, "certificates.cert-manager.io"))
	if err != nil {
		if IsForbiddenKubectlError(err) {
			return nil, fmt.Errorf("%w: %w", ErrListExposedServicesForbidden, err)
		}
		return nil, err
	}
	return FilterExposedServices(ingresses, certs), nil
}

// FilterExposedServices maps observed Ingresses to erun-expose exposures.
func FilterExposedServices(ingresses []ObservedIngress, certs []ObservedCertificate) []ExposedService {
	tlsSecretByHost := TlsSecretsByHost(ingresses)
	certBySecret := certificatesBySecret(certs)
	services := make([]ExposedService, 0, len(ingresses))
	for _, ing := range ingresses {
		service, ok := strings.CutPrefix(ing.Name, ExposeIngressNamePrefix)
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

// TlsSecretsByHost maps each TLS-terminated host to its Ingress's TLS Secret name.
func TlsSecretsByHost(ingresses []ObservedIngress) map[string]string {
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

// tlsReadiness reports an absent Certificate as not ready with a reason, never as success.
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

// certificateNotReadyReason prefers the deepest CertificateRequest/Order/Challenge
// reason over the Certificate's often generic condition.
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
