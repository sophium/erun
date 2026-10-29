package registry

import (
	"fmt"
	"strings"
)

// GHCR never accepts an anonymous push, so a missing credential is checked
// before the build rather than treated as inconclusive.

// MissingGHCRCredentialError means no credential source resolves for a ghcr.io push.
type MissingGHCRCredentialError struct {
	Registry string
}

func (e *MissingGHCRCredentialError) Error() string {
	registry := e.Registry
	if registry == "" {
		registry = "ghcr.io"
	}
	// docker login takes a bare host.
	host, _, _ := strings.Cut(registry, "/")
	return fmt.Sprintf(
		"no credential is configured for %s: no docker config entry, no gh session, and no GH_TOKEN/GITHUB_TOKEN.\n"+
			"GHCR never accepts an anonymous push, so this is checked before the build rather than at the push, after every image has been built for every architecture.\n"+
			"From a shell in this environment (erun open), authenticate with one of:\n"+
			"  gh auth login -h github.com -s write:packages,read:packages\n"+
			"  docker login %s\n"+
			"then re-run erun build --release (or erun push).",
		registry, host)
}

// VerifyGHCRCredentialConfigured checks that a ghcr.io tag has a credential; other registries pass.
func VerifyGHCRCredentialConfigured(tag string) error {
	registry := DockerRegistryFromImageTag(tag)
	if !IsGHCRRegistry(registry) {
		return nil
	}
	if _, ok := resolveGHCRBasicAuth(DockerNamespaceFromTag(tag)); ok {
		return nil
	}
	return &MissingGHCRCredentialError{Registry: registry}
}

// VerifyGHCRChartCredentialConfigured is the chart counterpart of VerifyGHCRCredentialConfigured.
func VerifyGHCRChartCredentialConfigured(ociRepo string) error {
	registry, path := splitOCIChartRepo(ociRepo)
	if !IsGHCRRegistry(registry) {
		return nil
	}
	if _, ok := resolveGHCRBasicAuth(namespaceFromOCIPath(path)); ok {
		return nil
	}
	return &MissingGHCRCredentialError{Registry: registry}
}
