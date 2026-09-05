package eruncommon

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// control_plane_version_drift.go answers a question neither `erun exec
// route-check` nor GET /v1/platform's bare version number answers alone:
// is a deployed erun-hosted control plane running the newest version erun's
// own registry has PUBLISHED -- deployed-vs-published, not deployed-vs-main
// (erun#2052). A route can merge, close its issue, and still 404 on every
// live plane for months because nothing compares "merged" against "rolled
// out"; version_drift.go's TenantVersionDrift answers the neighboring
// question (drift between environments in one tenant) but has no registry
// baseline to compare against, and route-check proves a route's reachability
// on one already-chosen plane without ever asking whether that plane itself
// is current.
//
// The console rides along on the same check (erun#2070): each plane's own
// GET /v1/platform response names its console's URL (PlatformInfo.ConsoleURL
// -- a plane and its console are always deployed together, never configured
// as separate aliases), and the console answers the identical
// deployed-vs-published question about itself at GET /version.json, a static
// file stamped from ERUN_VERSION at image build time (erun-devops/AGENTS.md's
// erun-console chart bullet). Both surfaces publish at the same erun release
// version, so the one registry lookup that establishes the plane's published
// baseline also answers for its console -- no second registry probe.

// ConsoleVersionStatus is one control plane's linked console's deployed
// version (GET /version.json, unauthenticated), compared against the same
// published baseline as its plane.
type ConsoleVersionStatus struct {
	URL string `json:"url,omitempty"`
	// Reachable reports whether GET /version.json answered at all. A console
	// erun cannot reach is never reported current: Version/Behind/Ahead stay
	// unset rather than guessed from silence.
	Reachable         bool   `json:"reachable"`
	UnreachableReason string `json:"unreachableReason,omitempty"`
	Version           string `json:"version,omitempty"`
	// Behind/Ahead carry the same meaning as ControlPlaneVersionStatus's own
	// fields, evaluated against the identical published baseline.
	Behind bool `json:"behind,omitempty"`
	Ahead  bool `json:"ahead,omitempty"`
}

// ControlPlaneVersionStatus is one configured erun-hosted control plane's
// deployed version, compared against the newest version erun's own registry
// has published.
type ControlPlaneVersionStatus struct {
	Alias  string `json:"alias"`
	APIURL string `json:"apiUrl,omitempty"`
	// Reachable reports whether GET /v1/platform answered at all. A plane
	// erun cannot reach is never reported current: Version/Behind/Ahead stay
	// unset rather than guessed from silence.
	Reachable         bool   `json:"reachable"`
	UnreachableReason string `json:"unreachableReason,omitempty"`
	Version           string `json:"version,omitempty"`
	// Behind is set only when both Version and the registry's published
	// latest stable parse as plain three-part semver, and Version orders
	// strictly below it -- routine drift: the plane simply has not been
	// rolled onto an already-published release.
	Behind bool `json:"behind,omitempty"`
	// Ahead is the opposite order: the plane runs something the registry has
	// never published. Reported distinctly from Behind because it is a more
	// alarming condition -- an unpublished build reached a live plane some
	// other way -- not routine drift.
	Ahead bool `json:"ahead,omitempty"`
	// Console is nil when the plane's own GET /v1/platform reported no
	// consoleUrl (nothing to check), never a guessed/defaulted value.
	Console *ConsoleVersionStatus `json:"console,omitempty"`
	// DuplicateAliases lists other configured aliases whose own api-url
	// resolved to the same backend address as this one -- they
	// are folded into this single entry instead of getting their own row, so
	// a backend behind two hostnames is reported, and counted for
	// --fail-on-drift, exactly once.
	DuplicateAliases []string `json:"duplicateAliases,omitempty"`
	// AdvertisedAPIURLMismatch is set only when this plane's own GET
	// /v1/platform names an apiUrl that resolves to a wholly different
	// address than the one erun actually just reached -- a benign
	// canonical/CNAME alias (the same address under a different hostname)
	// is never flagged, only a genuinely foreign backend.
	AdvertisedAPIURLMismatch string `json:"advertisedApiUrlMismatch,omitempty"`
}

// ControlPlaneVersionDrift is every configured erun-hosted control plane's
// deployed version, compared against the newest version erun's own registry
// has published.
type ControlPlaneVersionDrift struct {
	Planes []ControlPlaneVersionStatus `json:"planes,omitempty"`
	// PublishedVersion is the newest stable version found in erun's own
	// registry -- established from the registry itself on every run, never a
	// hand-maintained list that could drift from what is actually published.
	PublishedVersion string `json:"publishedVersion,omitempty"`
	// PublishedVersionError is set when the registry lookup itself failed.
	// Every plane below still reports its own reachability and version, but
	// carries no behind/ahead verdict -- absent evidence, not a guess.
	PublishedVersionError string `json:"publishedVersionError,omitempty"`
}

// ResolveControlPlaneVersionDrift compares every configured erun-hosted
// control plane's deployed version (GET /v1/platform, unauthenticated)
// against the newest version erun's own registry has published. Under
// ctx.DryRun neither call is made -- every plane that would be probed, and
// the registry lookup that would establish the published baseline, are
// traced instead, matching route-check's own no-network-call dry-run
// contract (erun-common/route_check.go). deps is normalized the same way
// every other CloudDependencies-accepting entrypoint normalizes it, so a
// caller passing DefaultCloudDependencies() (whose FetchPlatformInfo starts
// nil) gets the real unauthenticated call.
func ResolveControlPlaneVersionDrift(ctx Context, result ListResult, deps CloudDependencies, resolvePublished RuntimeRegistryVersionResolverFunc) ControlPlaneVersionDrift {
	deps = normalizeCloudDependencies(deps)
	planes := controlPlaneProviders(result.CloudProviders)

	if ctx.DryRun {
		traceControlPlaneVersionDriftDryRun(ctx, planes)
		return ControlPlaneVersionDrift{}
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
	publishedSemver, publishedOK := parseRegistryStableVersion(drift.PublishedVersion)

	for _, group := range groupControlPlanesByBackend(ctx, planes, deps.ResolveHostAddrs) {
		status := resolveOneControlPlaneVersionStatus(ctx, group.representative, group.endpoints, deps.FetchPlatformInfo, deps.FetchConsoleVersion, deps.ResolveHostAddrs, publishedSemver, publishedOK)
		status.DuplicateAliases = group.duplicateAliases
		drift.Planes = append(drift.Planes, status)
	}
	return drift
}

// controlPlaneGroup is every configured alias erun determined resolves to one
// backend: representative is the first alias seen for that backend (the one
// actually probed), duplicateAliases the rest (folded in, never probed
// separately). endpoints is the representative's own resolved address:port
// set, carried forward so resolveOneControlPlaneVersionStatus can reuse it for
// the discovery-document mismatch check without re-resolving.
type controlPlaneGroup struct {
	representative   CloudProviderStatus
	duplicateAliases []string
	endpoints        []string
}

// groupControlPlanesByBackend folds configured aliases that resolve to the
// same backend address into one group: a plane and a vanity
// hostname that CNAMEs to it are one deployment, not two, and reporting both
// separately double-counts every drift finding. An alias whose host cannot be
// resolved is never folded into, or merged with, anything -- absent evidence
// of identity, not a guess.
func groupControlPlanesByBackend(ctx Context, planes []CloudProviderStatus, resolveHostAddrs func(Context, string) ([]string, error)) []controlPlaneGroup {
	groups := make([]controlPlaneGroup, 0, len(planes))
	for _, provider := range planes {
		endpoints, err := resolveControlPlaneEndpoints(ctx, controlPlaneAPIURL(provider), resolveHostAddrs)
		if err != nil {
			ctx.Trace("list: could not resolve control plane " + provider.Alias + "'s own host, so it cannot be checked for duplicate-backend aliases: " + err.Error())
			groups = append(groups, controlPlaneGroup{representative: provider})
			continue
		}
		if idx := findControlPlaneGroupByEndpoints(groups, endpoints); idx >= 0 {
			ctx.Trace("list: " + provider.Alias + " resolves to the same backend as " + groups[idx].representative.Alias + " -- reporting it once")
			groups[idx].duplicateAliases = append(groups[idx].duplicateAliases, provider.Alias)
			continue
		}
		groups = append(groups, controlPlaneGroup{representative: provider, endpoints: endpoints})
	}
	return groups
}

func findControlPlaneGroupByEndpoints(groups []controlPlaneGroup, endpoints []string) int {
	for i, group := range groups {
		if endpointsIntersect(group.endpoints, endpoints) {
			return i
		}
	}
	return -1
}

// resolveControlPlaneEndpoints resolves an api url's host to its
// address:port pairs -- the identity groupControlPlanesByBackend and the
// mismatch check below compare, since two hostnames naming the same backend
// share these even when the hostnames themselves differ.
func resolveControlPlaneEndpoints(ctx Context, apiURL string, resolveHostAddrs func(Context, string) ([]string, error)) ([]string, error) {
	apiURL = strings.TrimSpace(apiURL)
	if apiURL == "" {
		return nil, fmt.Errorf("no api url configured")
	}
	parsed, err := url.Parse(apiURL)
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

// defaultResolveHostAddrs is the real DNS lookup behind CloudDependencies'
// ResolveHostAddrs. It ignores ctx.DryRun deliberately: callers only invoke it
// from real-run code paths (ResolveControlPlaneVersionDrift returns before
// reaching it under --dry-run), so there is no dry-run contract for it to
// honor here the way defaultFetchConsoleVersion's own DryRun short-circuit
// exists for its call site alone.
func defaultResolveHostAddrs(_ Context, host string) ([]string, error) {
	return net.DefaultResolver.LookupHost(context.Background(), host)
}

// controlPlaneProviders filters providers down to the erun-hosted ones --
// the only kind GET /v1/platform applies to.
func controlPlaneProviders(providers []CloudProviderStatus) []CloudProviderStatus {
	planes := make([]CloudProviderStatus, 0, len(providers))
	for _, provider := range providers {
		if provider.Provider == CloudProviderERun {
			planes = append(planes, provider)
		}
	}
	return planes
}

func controlPlaneAPIURL(provider CloudProviderStatus) string {
	if provider.ERun == nil {
		return ""
	}
	return strings.TrimSpace(provider.ERun.APIURL)
}

func traceControlPlaneVersionDriftDryRun(ctx Context, planes []CloudProviderStatus) {
	ctx.Trace("list: would resolve the published version from erun's own registry")
	for _, provider := range planes {
		ctx.Trace("list: would GET " + controlPlaneAPIURL(provider) + "/v1/platform for control plane " + provider.Alias)
		ctx.Trace("list: would also probe " + provider.Alias + "'s console at /version.json, using the consoleUrl that GET /v1/platform discloses")
	}
}

func resolveOneControlPlaneVersionStatus(ctx Context, provider CloudProviderStatus, ownEndpoints []string, fetchPlatformInfo func(Context, string) (PlatformInfo, error), fetchConsoleVersion func(Context, string) (string, error), resolveHostAddrs func(Context, string) ([]string, error), publishedSemver semver, publishedOK bool) ControlPlaneVersionStatus {
	apiURL := controlPlaneAPIURL(provider)
	status := ControlPlaneVersionStatus{Alias: provider.Alias, APIURL: apiURL}
	if apiURL == "" {
		status.UnreachableReason = "control plane alias has no configured api url"
		return status
	}

	ctx.Trace("list: GET " + apiURL + "/v1/platform (control plane version check for " + provider.Alias + ")")
	info, err := fetchPlatformInfo(ctx, apiURL)
	if err != nil {
		status.UnreachableReason = err.Error()
		ctx.Trace("list: control plane " + provider.Alias + " unreachable: " + err.Error())
		return status
	}

	status.Reachable = true
	status.Version = strings.TrimSpace(info.Version)
	status.Behind, status.Ahead = versionVerdict(status.Version, publishedSemver, publishedOK)
	status.AdvertisedAPIURLMismatch = detectAdvertisedAPIURLMismatch(ctx, provider.Alias, apiURL, info.APIURL, ownEndpoints, resolveHostAddrs)

	if consoleURL := strings.TrimSpace(info.ConsoleURL); consoleURL != "" {
		console := resolveConsoleVersionStatus(ctx, provider.Alias, consoleURL, fetchConsoleVersion, publishedSemver, publishedOK)
		status.Console = &console
	}
	return status
}

// detectAdvertisedAPIURLMismatch answers the question a discovery document
// with the wrong apiUrl is actually about: does this plane's own discovery document (GET /v1/platform's
// apiUrl field) name a backend erun did not just reach? A textually different
// apiUrl is common and benign -- a vanity hostname CNAMEing to the one erun
// queried -- so this only flags the case that difference cannot explain: the
// discovered hostname resolving to no address this plane's own configured
// host shares. Resolution failure on either side, or on ownEndpoints not
// having been resolvable at all, means no verdict rather than a guess.
func detectAdvertisedAPIURLMismatch(ctx Context, alias, ownAPIURL, discoveredAPIURL string, ownEndpoints []string, resolveHostAddrs func(Context, string) ([]string, error)) string {
	discoveredAPIURL = strings.TrimRight(strings.TrimSpace(discoveredAPIURL), "/")
	ownAPIURL = strings.TrimRight(strings.TrimSpace(ownAPIURL), "/")
	if discoveredAPIURL == "" || discoveredAPIURL == ownAPIURL || len(ownEndpoints) == 0 {
		return ""
	}
	discoveredEndpoints, err := resolveControlPlaneEndpoints(ctx, discoveredAPIURL, resolveHostAddrs)
	if err != nil {
		ctx.Trace("list: could not resolve " + alias + "'s discovered apiUrl " + discoveredAPIURL + ", so it cannot be checked against the address actually reached: " + err.Error())
		return ""
	}
	if endpointsIntersect(ownEndpoints, discoveredEndpoints) {
		return ""
	}
	return "GET /v1/platform's own apiUrl (" + discoveredAPIURL + ") resolves to a different address than " + alias + " itself -- it may be advertising a different plane's api"
}

func resolveConsoleVersionStatus(ctx Context, alias, consoleURL string, fetchConsoleVersion func(Context, string) (string, error), publishedSemver semver, publishedOK bool) ConsoleVersionStatus {
	status := ConsoleVersionStatus{URL: consoleURL}
	ctx.Trace("list: GET " + consoleURL + "/version.json (console version check for " + alias + ")")
	version, err := fetchConsoleVersion(ctx, consoleURL)
	if err != nil {
		status.UnreachableReason = err.Error()
		ctx.Trace("list: console for " + alias + " unreachable: " + err.Error())
		return status
	}

	status.Reachable = true
	status.Version = strings.TrimSpace(version)
	status.Behind, status.Ahead = versionVerdict(status.Version, publishedSemver, publishedOK)
	return status
}

// versionVerdict compares a deployed version against the published baseline,
// the shared logic behind both ControlPlaneVersionStatus and
// ConsoleVersionStatus's Behind/Ahead fields.
func versionVerdict(version string, publishedSemver semver, publishedOK bool) (behind, ahead bool) {
	if !publishedOK {
		return false, false
	}
	deployedSemver, deployedOK := parseRegistryStableVersion(version)
	if !deployedOK {
		return false, false
	}
	switch {
	case compareSemver(deployedSemver, publishedSemver) < 0:
		return true, false
	case compareSemver(deployedSemver, publishedSemver) > 0:
		return false, true
	}
	return false, false
}

// defaultFetchConsoleVersion resolves a deployed console's own build version
// via its unauthenticated GET /version.json -- the console's counterpart to
// defaultFetchPlatformInfo's GET /v1/platform.
func defaultFetchConsoleVersion(ctx Context, consoleURL string) (string, error) {
	target := strings.TrimRight(strings.TrimSpace(consoleURL), "/") + "/version.json"
	ctx.Trace("GET " + target)
	if ctx.DryRun {
		return "", nil
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := fetchJSON(target, &body); err != nil {
		return "", fmt.Errorf("fetch console version: %w", err)
	}
	return body.Version, nil
}

func valueOrNoneLabel(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(none)"
	}
	return value
}
