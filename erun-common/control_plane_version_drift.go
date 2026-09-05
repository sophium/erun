package eruncommon

import (
	"context"
	"fmt"
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

// ControlPlaneAliasRef is one configured cloud-provider alias erun resolved
// to the same backend as another configured alias's own -- see
// ControlPlaneVersionStatus.AdditionalAliases. Pointing two aliases at one
// plane is legitimate configuration (erun#2089); this is how the collapsed
// report still names every alias that reaches it.
type ControlPlaneAliasRef struct {
	Alias  string `json:"alias"`
	APIURL string `json:"apiUrl,omitempty"`
}

// ControlPlaneVersionStatus is one configured erun-hosted control plane's
// deployed version, compared against the newest version erun's own registry
// has published.
type ControlPlaneVersionStatus struct {
	Alias  string `json:"alias"`
	APIURL string `json:"apiUrl,omitempty"`
	// AdditionalAliases lists every other configured alias erun resolved to
	// this exact same backend deployment (see controlPlaneBackendIdentity) --
	// e.g. two DNS names that both route to one physical control plane.
	// Reporting each as its own plane would double-count drift an operator
	// would only ever act on once, so every alias sharing a backend is
	// collapsed into this one entry instead of appearing as a separate plane
	// (erun#2089).
	AdditionalAliases []ControlPlaneAliasRef `json:"additionalAliases,omitempty"`
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

	byIdentity := map[string]int{}
	for _, provider := range planes {
		resolveOneControlPlaneVersionStatus(ctx, &drift, byIdentity, provider, deps.FetchPlatformInfo, deps.FetchConsoleVersion, publishedSemver, publishedOK)
	}
	return drift
}

// appendControlPlaneAlias is where collapsing actually happens: a backend
// identity already seen merges this alias into the existing plane's
// AdditionalAliases instead of appending a new plane, so drift.Planes always
// has exactly one entry per real deployment, not per configured alias.
func appendControlPlaneAlias(drift *ControlPlaneVersionDrift, byIdentity map[string]int, identity string, status ControlPlaneVersionStatus) {
	if idx, ok := byIdentity[identity]; ok {
		drift.Planes[idx].AdditionalAliases = append(drift.Planes[idx].AdditionalAliases, ControlPlaneAliasRef{Alias: status.Alias, APIURL: status.APIURL})
		return
	}
	byIdentity[identity] = len(drift.Planes)
	drift.Planes = append(drift.Planes, status)
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

// resolveOneControlPlaneVersionStatus fetches one alias's GET /v1/platform
// and either merges it into an already-seen backend's plane entry (see
// controlPlaneBackendIdentity) or appends a new one. The identity check runs
// before the console is ever probed, so a duplicate alias costs one wasted
// GET /v1/platform, not a second GET /version.json for a console already
// checked under the first alias.
func resolveOneControlPlaneVersionStatus(ctx Context, drift *ControlPlaneVersionDrift, byIdentity map[string]int, provider CloudProviderStatus, fetchPlatformInfo func(Context, string) (PlatformInfo, error), fetchConsoleVersion func(Context, string) (string, error), publishedSemver semver, publishedOK bool) {
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

	if consoleURL := strings.TrimSpace(info.ConsoleURL); consoleURL != "" {
		console := resolveConsoleVersionStatus(ctx, provider.Alias, consoleURL, fetchConsoleVersion, publishedSemver, publishedOK)
		status.Console = &console
	}
	appendControlPlaneAlias(drift, byIdentity, identity, status)
}

// controlPlaneBackendIdentity is the key duplicate-alias collapsing groups
// on: the plane's own self-declared apiUrl from GET /v1/platform when it
// reported one -- a real identity read from the discovery document itself,
// set once in the backend's own config regardless of which hostname a client
// dialed to reach it, so two aliases pointed at one physical deployment
// report the identical value. When the plane never answered, or answered
// with no apiUrl (an older platform), erun falls back to the alias's own
// configured URL verbatim -- never DNS. Two hostnames resolving to the same
// address(es) is not proof of a shared backend: a multi-tenant cluster
// commonly fronts many distinct control planes behind one shared ingress
// IP:port, routed by hostname/SNI, so a DNS-only match can merge two
// deployments that happen to sit behind the same load balancer. That is
// exactly the case where collapsing is most dangerous -- it is taken only
// when the plane is unreachable or too old to self-report, which is when
// --fail-on-drift most needs to see it -- so an uncertain signal never
// collapses. Reporting two rows for one backend is a cosmetic annoyance;
// reporting one row for two backends hides a stale deployment. Deliberately
// never keys on version either: two genuinely distinct planes can run the
// same published release (erun#2089).
func controlPlaneBackendIdentity(configuredAPIURL, serverReportedAPIURL string) string {
	if normalized := normalizeControlPlaneIdentityURL(serverReportedAPIURL); normalized != "" {
		return "url:" + normalized
	}
	return "url:" + normalizeControlPlaneIdentityURL(configuredAPIURL)
}

// normalizeControlPlaneIdentityURL reduces a URL to a lower-cased
// scheme+host, dropping path/query/fragment so two aliases configured with
// e.g. a trailing slash or path difference still compare equal. Falls back to
// a trimmed, lower-cased copy of the raw value when it does not parse as a
// URL with a host, rather than discarding a real (if unusual) value.
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
