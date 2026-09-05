package eruncommon

import (
	"fmt"
	"strings"
)

// VerifyGHCRPushScope and VerifyGHCRCanPushImage/VerifyGHCRCanPushChart both
// treat "no credential resolved" as inconclusive -- the same bucket as a
// network hiccup or an unreachable registry -- so a release proceeds to spend
// a full multi-arch build on a pod that has never once authenticated to
// ghcr.io. That is not actually ambiguous: GHCR never accepts an anonymous
// push, to any repository, public or private, so a pod with no docker config
// entry, no gh session, and no GH_TOKEN/GITHUB_TOKEN is certain to fail at the
// real push. This check answers that certain case up front, before the other
// two (which need a resolved credential to say anything at all), so it is
// checked first (#1201).

// MissingGHCRCredentialError is returned when no credential source resolves
// for a ghcr.io push at all. It is distinct from MissingGHCRPushScopeError
// (a credential exists but lacks write:packages) and
// MissingGHCRCreatePackageError (a credential exists but the registry denies
// this specific repository) -- both of those need a credential to evaluate;
// this is what fires when there is none to evaluate.
type MissingGHCRCredentialError struct {
	Registry string
}

func (e *MissingGHCRCredentialError) Error() string {
	registry := e.Registry
	if registry == "" {
		registry = "ghcr.io"
	}
	// docker login takes a bare host, never a namespace path, so the remedy
	// command strips anything after ghcr.io even when Registry carries one
	// (e.g. "ghcr.io/sophium") for a more specific diagnosis above.
	host, _, _ := strings.Cut(registry, "/")
	return fmt.Sprintf(
		"no credential is configured for %s: no docker config entry, no gh session, and no GH_TOKEN/GITHUB_TOKEN.\n"+
			"GHCR never accepts an anonymous push, so this is checked before the build rather than at the push, after every image has been built for every architecture.\n"+
			"From a shell in this environment (erun open), authenticate with one of:\n"+
			"  gh auth login -h github.com -s write:packages,read:packages\n"+
			"  docker login %s\n"+
			"then re-run erun release (or erun push).",
		registry, host)
}

// VerifyGHCRCredentialConfigured reports whether any credential source
// resolves for the ghcr.io registry an image tag names. It answers only for
// ghcr; a tag naming another registry is always nil.
func VerifyGHCRCredentialConfigured(tag string) error {
	registry := dockerRegistryFromImageTag(tag)
	if !isGHCRRegistry(registry) {
		return nil
	}
	if _, ok := resolveGHCRBasicAuth(DockerNamespaceFromTag(tag)); ok {
		return nil
	}
	return &MissingGHCRCredentialError{Registry: registry}
}

// VerifyGHCRChartCredentialConfigured is the chart entry point, mirroring
// VerifyGHCRCredentialConfigured for a chart's OCI repository.
func VerifyGHCRChartCredentialConfigured(ociRepo string) error {
	registry, path := splitOCIChartRepo(ociRepo)
	if !isGHCRRegistry(registry) {
		return nil
	}
	if _, ok := resolveGHCRBasicAuth(namespaceFromOCIPath(path)); ok {
		return nil
	}
	return &MissingGHCRCredentialError{Registry: registry}
}
