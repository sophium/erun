package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/platformclient"
	"github.com/sophium/erun/erun-common/infra/registry"
)

// Compares each deployed erun-hosted control plane, and the console and docs site it
// links, against the newest version erun's registry has published.

// VersionSurfaceStatus is a console or docs site's deployed version (GET /version.json).
type VersionSurfaceStatus struct {
	URL string `json:"url,omitempty"`
	// An unreachable surface is never reported current; Version/Behind/Ahead stay unset.
	Reachable         bool   `json:"reachable"`
	UnreachableReason string `json:"unreachableReason,omitempty"`
	Version           string `json:"version,omitempty"`
	// Reason explains a Version of "unknown": the surface answered, but not with the
	// expected JSON (often an SPA index.html fallback). It is never Reachable=false.
	Reason string `json:"reason,omitempty"`
	Behind bool   `json:"behind,omitempty"`
	Ahead  bool   `json:"ahead,omitempty"`
}

// ControlPlaneAliasRef is a configured alias that resolved to an already-reported backend.
type ControlPlaneAliasRef struct {
	Alias  string `json:"alias"`
	APIURL string `json:"apiUrl,omitempty"`
}

// ControlPlaneVersionStatus is one control plane's deployed version against the published one.
type ControlPlaneVersionStatus struct {
	Alias  string `json:"alias"`
	APIURL string `json:"apiUrl,omitempty"`
	// AdditionalAliases are other aliases on the same backend, collapsed so drift is not double-counted.
	AdditionalAliases []ControlPlaneAliasRef `json:"additionalAliases,omitempty"`
	// An unreachable plane is never reported current; Version/Behind/Ahead stay unset.
	Reachable         bool   `json:"reachable"`
	UnreachableReason string `json:"unreachableReason,omitempty"`
	Version           string `json:"version,omitempty"`
	// Behind and Ahead are set only when both versions parse as plain semver.
	Behind bool `json:"behind,omitempty"`
	// Ahead means the plane runs a build the registry never published.
	Ahead bool `json:"ahead,omitempty"`
	// Console is nil when the plane reported no consoleUrl.
	Console *VersionSurfaceStatus `json:"console,omitempty"`
	// Docs is nil when the plane reported no docsUrl.
	Docs *VersionSurfaceStatus `json:"docs,omitempty"`
	// AdvertisedAPIURLMismatch is the plane's advertised apiUrl when it resolves to no address
	// the dialed host shares; a host unresolvable on either side yields no verdict.
	AdvertisedAPIURLMismatch string `json:"advertisedApiUrlMismatch,omitempty"`
}

// ControlPlaneVersionDrift is every configured control plane's version status.
type ControlPlaneVersionDrift struct {
	Planes []ControlPlaneVersionStatus `json:"planes,omitempty"`
	// PublishedVersion is read from the registry on every run.
	PublishedVersion string `json:"publishedVersion,omitempty"`
	// PublishedVersionError leaves every plane without a behind/ahead verdict.
	PublishedVersionError string `json:"publishedVersionError,omitempty"`
}

// ResolveControlPlaneVersionDrift compares each erun-hosted plane (or only alias) against the
// published version; under DryRun it makes no network calls.
func ResolveControlPlaneVersionDrift(ctx core.Context, result ListResult, alias string, deps cloud.CloudDependencies, resolvePublished registry.RuntimeRegistryVersionResolverFunc) (ControlPlaneVersionDrift, error) {
	deps = cloud.NormalizeCloudDependencies(deps)
	planes, err := selectControlPlaneProviders(result.CloudProviders, alias)
	if err != nil {
		return ControlPlaneVersionDrift{}, err
	}

	if ctx.DryRun {
		traceControlPlaneVersionDriftDryRun(ctx, planes)
		return ControlPlaneVersionDrift{}, nil
	}

	ctx.Trace("list: resolving the published version from erun's own registry")
	drift := ControlPlaneVersionDrift{}
	published, err := resolvePublished(context.Background())
	if err != nil {
		drift.PublishedVersionError = err.Error()
		ctx.Trace("list: could not resolve the published version: " + err.Error())
	} else {
		drift.PublishedVersion = strings.TrimSpace(published.LatestStable)
		ctx.Trace("list: latest published version is " + valueOrNoneLabel(drift.PublishedVersion))
	}
	publishedSemver, publishedOK := registry.ParseRegistryStableVersion(drift.PublishedVersion)

	byIdentity := map[string]int{}
	for _, provider := range planes {
		resolveOneControlPlaneVersionStatus(ctx, &drift, byIdentity, provider, deps.FetchPlatformInfo, deps.FetchVersionJSON, deps.ResolveHostAddrs, publishedSemver, publishedOK)
	}
	return drift, nil
}

// appendControlPlaneAlias keeps one plane entry per backend identity.
func appendControlPlaneAlias(drift *ControlPlaneVersionDrift, byIdentity map[string]int, identity string, status ControlPlaneVersionStatus) {
	if idx, ok := byIdentity[identity]; ok {
		drift.Planes[idx].AdditionalAliases = append(drift.Planes[idx].AdditionalAliases, ControlPlaneAliasRef{Alias: status.Alias, APIURL: status.APIURL})
		return
	}
	byIdentity[identity] = len(drift.Planes)
	drift.Planes = append(drift.Planes, status)
}

func controlPlaneProviders(providers []cloud.CloudProviderStatus) []cloud.CloudProviderStatus {
	planes := make([]cloud.CloudProviderStatus, 0, len(providers))
	for _, provider := range providers {
		if provider.Provider == config.CloudProviderERun {
			planes = append(planes, provider)
		}
	}
	return planes
}

// Error text matches ResolveCloudProvider so a bad --erun-alias reads the same everywhere.
func selectControlPlaneProviders(providers []cloud.CloudProviderStatus, alias string) ([]cloud.CloudProviderStatus, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return controlPlaneProviders(providers), nil
	}
	for _, provider := range providers {
		if provider.Alias != alias {
			continue
		}
		if provider.Provider != config.CloudProviderERun {
			return nil, fmt.Errorf("cloud provider alias %q is a %q-type alias, not an erun platform alias", provider.Alias, provider.Provider)
		}
		return []cloud.CloudProviderStatus{provider}, nil
	}
	return nil, fmt.Errorf("cloud provider alias %q is not configured", alias)
}

func controlPlaneAPIURL(provider cloud.CloudProviderStatus) string {
	if provider.ERun == nil {
		return ""
	}
	return strings.TrimSpace(provider.ERun.APIURL)
}

func traceControlPlaneVersionDriftDryRun(ctx core.Context, planes []cloud.CloudProviderStatus) {
	ctx.Trace("list: would resolve the published version from erun's own registry")
	for _, provider := range planes {
		ctx.Trace("list: would GET " + controlPlaneAPIURL(provider) + "/v1/platform for control plane " + provider.Alias)
		ctx.Trace("list: would also probe " + provider.Alias + "'s console and docs site at /version.json, using the consoleUrl and docsUrl that GET /v1/platform discloses")
	}
}

// The identity check runs before probing the console so a duplicate alias costs no extra probe.
func resolveOneControlPlaneVersionStatus(ctx core.Context, drift *ControlPlaneVersionDrift, byIdentity map[string]int, provider cloud.CloudProviderStatus, fetchPlatformInfo func(core.Context, string) (platformclient.PlatformInfo, error), fetchVersionJSON func(core.Context, string) (string, error), resolveHostAddrs func(core.Context, string) ([]string, error), publishedSemver registry.Semver, publishedOK bool) {
	apiURL := controlPlaneAPIURL(provider)
	if apiURL == "" {
		appendControlPlaneAlias(drift, byIdentity, "no-api-url:"+provider.Alias, ControlPlaneVersionStatus{
			Alias:             provider.Alias,
			UnreachableReason: "control plane alias has no configured api url",
		})
		return
	}

	ctx.Trace("list: GET " + apiURL + "/v1/platform (control plane version check for " + provider.Alias + ")")
	info, err := fetchPlatformInfo(ctx, apiURL)
	if err != nil {
		ctx.Trace("list: control plane " + provider.Alias + " unreachable: " + err.Error())
		identity := controlPlaneBackendIdentity(apiURL, "")
		appendControlPlaneAlias(drift, byIdentity, identity, ControlPlaneVersionStatus{
			Alias:             provider.Alias,
			APIURL:            apiURL,
			UnreachableReason: err.Error(),
		})
		return
	}

	identity := controlPlaneBackendIdentity(apiURL, info.APIURL)
	if _, alreadySeen := byIdentity[identity]; alreadySeen {
		appendControlPlaneAlias(drift, byIdentity, identity, ControlPlaneVersionStatus{Alias: provider.Alias, APIURL: apiURL})
		return
	}

	status := ControlPlaneVersionStatus{Alias: provider.Alias, APIURL: apiURL, Reachable: true}
	status.Version = strings.TrimSpace(info.Version)
	status.Behind, status.Ahead = versionVerdict(status.Version, publishedSemver, publishedOK)
	status.AdvertisedAPIURLMismatch = detectAdvertisedAPIURLMismatch(ctx, provider.Alias, apiURL, info.APIURL, resolveHostAddrs)

	if consoleURL := strings.TrimSpace(info.ConsoleURL); consoleURL != "" {
		console := resolveVersionSurfaceStatus(ctx, provider.Alias, versionSurfaceConsole, consoleURL, fetchVersionJSON, publishedSemver, publishedOK)
		status.Console = &console
	}
	if docsURL := strings.TrimSpace(info.DocsURL); docsURL != "" {
		docs := resolveVersionSurfaceStatus(ctx, provider.Alias, versionSurfaceDocs, docsURL, fetchVersionJSON, publishedSemver, publishedOK)
		status.Docs = &docs
	}
	appendControlPlaneAlias(drift, byIdentity, identity, status)
}

// controlPlaneBackendIdentity prefers the plane's self-reported apiUrl, else the configured URL.
// Never DNS: shared ingress IPs front distinct planes, and merging two backends hides a stale one.
func controlPlaneBackendIdentity(configuredAPIURL, serverReportedAPIURL string) string {
	if normalized := normalizeControlPlaneIdentityURL(serverReportedAPIURL); normalized != "" {
		return "url:" + normalized
	}
	return "url:" + normalizeControlPlaneIdentityURL(configuredAPIURL)
}

func normalizeControlPlaneIdentityURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return strings.ToLower(strings.TrimRight(trimmed, "/"))
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host)
}

// detectAdvertisedAPIURLMismatch flags only an advertised apiUrl sharing no address with the
// dialed host; resolver errors are not traced since they embed the machine's resolver address.
func detectAdvertisedAPIURLMismatch(ctx core.Context, alias, ownAPIURL, discoveredAPIURL string, resolveHostAddrs func(core.Context, string) ([]string, error)) string {
	discoveredAPIURL = strings.TrimRight(strings.TrimSpace(discoveredAPIURL), "/")
	ownAPIURL = strings.TrimRight(strings.TrimSpace(ownAPIURL), "/")
	if discoveredAPIURL == "" || discoveredAPIURL == ownAPIURL {
		return ""
	}
	ownEndpoints, err := resolveControlPlaneEndpoints(ctx, ownAPIURL, resolveHostAddrs)
	if err != nil {
		ctx.Trace("list: could not resolve " + alias + "'s own api url " + ownAPIURL + ", so its discovery document's apiUrl cannot be checked against the address actually reached")
		return ""
	}
	discoveredEndpoints, err := resolveControlPlaneEndpoints(ctx, discoveredAPIURL, resolveHostAddrs)
	if err != nil {
		ctx.Trace("list: could not resolve " + alias + "'s discovered apiUrl " + discoveredAPIURL + ", so it cannot be checked against the address actually reached")
		return ""
	}
	if endpointsIntersect(ownEndpoints, discoveredEndpoints) {
		return ""
	}
	return "GET /v1/platform's own apiUrl (" + discoveredAPIURL + ") resolves to a different address than " + alias + " itself -- it may be advertising a different plane's api"
}

func resolveControlPlaneEndpoints(ctx core.Context, apiURL string, resolveHostAddrs func(core.Context, string) ([]string, error)) ([]string, error) {
	parsed, err := url.Parse(strings.TrimSpace(apiURL))
	if err != nil {
		return nil, fmt.Errorf("parse api url: %w", err)
	}
	host := parsed.Hostname()
	if host == "" {
		return nil, fmt.Errorf("api url %q has no host", apiURL)
	}
	port := parsed.Port()
	if port == "" {
		port = defaultPortForScheme(parsed.Scheme)
	}
	addrs, err := resolveHostAddrs(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", host, err)
	}
	endpoints := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		endpoints = append(endpoints, net.JoinHostPort(addr, port))
	}
	return endpoints, nil
}

func defaultPortForScheme(scheme string) string {
	if strings.EqualFold(scheme, "http") {
		return "80"
	}
	return "443"
}

func endpointsIntersect(a, b []string) bool {
	seen := make(map[string]struct{}, len(a))
	for _, endpoint := range a {
		seen[endpoint] = struct{}{}
	}
	for _, endpoint := range b {
		if _, ok := seen[endpoint]; ok {
			return true
		}
	}
	return false
}

const (
	versionSurfaceConsole = "console"
	versionSurfaceDocs    = "docs site"
)

func resolveVersionSurfaceStatus(ctx core.Context, alias, surface, surfaceURL string, fetchVersionJSON func(core.Context, string) (string, error), publishedSemver registry.Semver, publishedOK bool) VersionSurfaceStatus {
	status := VersionSurfaceStatus{URL: surfaceURL}
	ctx.Trace("list: GET " + surfaceURL + "/version.json (" + surface + " version check for " + alias + ")")
	version, err := fetchVersionJSON(ctx, surfaceURL)
	if err != nil {
		var contentErr *cloud.VersionSurfaceUnexpectedContentError
		if errors.As(err, &contentErr) {
			// The surface answered, so it is reachable; the fault is the document it served.
			status.Reachable = true
			status.Version = "unknown"
			status.Reason = contentErr.Error()
			ctx.Trace("list: " + surface + " for " + alias + " reachable but did not serve the expected document: " + contentErr.Error())
			return status
		}
		// Name the surface so a plane with both console and docs reports which one failed.
		reason := "fetch " + surface + " version: " + err.Error()
		status.UnreachableReason = reason
		ctx.Trace("list: " + surface + " for " + alias + " unreachable: " + reason)
		return status
	}

	status.Reachable = true
	status.Version = strings.TrimSpace(version)
	status.Behind, status.Ahead = versionVerdict(status.Version, publishedSemver, publishedOK)
	return status
}

func versionVerdict(version string, publishedSemver registry.Semver, publishedOK bool) (behind, ahead bool) {
	if !publishedOK {
		return false, false
	}
	deployedSemver, deployedOK := registry.ParseRegistryStableVersion(version)
	if !deployedOK {
		return false, false
	}
	switch {
	case registry.CompareSemver(deployedSemver, publishedSemver) < 0:
		return true, false
	case registry.CompareSemver(deployedSemver, publishedSemver) > 0:
		return false, true
	}
	return false, false
}

func valueOrNoneLabel(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(none)"
	}
	return value
}
