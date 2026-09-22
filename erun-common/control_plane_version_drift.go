package eruncommon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
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
// The surfaces a plane links to ride along on the same check (erun#2070): each
// plane's own GET /v1/platform response names both its console's URL
// (PlatformInfo.ConsoleURL) and its documentation site's
// (PlatformInfo.DocsURL) -- neither is configured as a separate alias -- and
// each answers the identical deployed-vs-published question about itself at
// GET /version.json, a static file stamped from ERUN_VERSION at image build
// time (erun-devops/AGENTS.md's erun-console chart bullet; the docs site
// publishes the same file through Cloudflare Pages). Every surface ships at
// the same erun release version, so the one registry lookup that establishes
// the plane's published baseline also answers for its console and its docs
// site -- no second registry probe.
//
// The docs site is checked as its own surface rather than folded into the
// console's because the two deploy independently: a console can be current
// while the docs site a deploy left behind still serves the release before it,
// and one verdict covering both would report whichever it read.

// VersionSurfaceStatus is one version surface a control plane links to -- its
// console or its documentation site -- and its deployed version (GET
// /version.json, unauthenticated), compared against the same published
// baseline as its plane.
type VersionSurfaceStatus struct {
	URL string `json:"url,omitempty"`
	// Reachable reports whether GET /version.json answered at all. A surface
	// erun cannot reach is never reported current: Version/Behind/Ahead stay
	// unset rather than guessed from silence.
	Reachable         bool   `json:"reachable"`
	UnreachableReason string `json:"unreachableReason,omitempty"`
	Version           string `json:"version,omitempty"`
	// Reason explains a Version of "unknown" on an otherwise-reachable
	// surface: GET /version.json answered, but not with the expected JSON
	// document (most often an SPA's index.html fallback for a route the
	// deployed nginx config doesn't yet exact-match). Distinct from
	// UnreachableReason -- the surface did answer, so this is a content
	// problem, not a reachability problem, and must never be reported as
	// Reachable=false.
	Reason string `json:"reason,omitempty"`
	// Behind/Ahead carry the same meaning as ControlPlaneVersionStatus's own
	// fields, evaluated against the identical published baseline.
	Behind bool `json:"behind,omitempty"`
	Ahead  bool `json:"ahead,omitempty"`
}

// ControlPlaneAliasRef is one configured cloud-provider alias erun resolved
// to the same backend as another configured alias's own -- see
// ControlPlaneVersionStatus.AdditionalAliases. Pointing two aliases at one
// plane is legitimate configuration; this is how the collapsed
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
	// collapsed into this one entry instead of appearing as a separate plane.
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
	Console *VersionSurfaceStatus `json:"console,omitempty"`
	// Docs is nil when the plane's own GET /v1/platform reported no docsUrl.
	// An absent docs site reads as absent -- never as one that is current,
	// and never as a plane that is behind on accounts of it.
	Docs *VersionSurfaceStatus `json:"docs,omitempty"`
	// AdvertisedAPIURLMismatch is the plane's own discovery document's apiUrl
	// when it resolves to a different address than the one erun actually
	// reached for this alias, and empty otherwise. A textually different
	// apiUrl is common and benign -- a vanity hostname CNAMEing to the one
	// erun queried -- so only a difference resolution cannot explain is
	// flagged, and a host that does not resolve on either side means no
	// verdict rather than a guess. Unlike Behind/Ahead this is never routine
	// drift: it is a plane pointing at a backend other than its own.
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
//
// alias, when non-empty, narrows the check to that one configured alias
// instead of every configured erun-hosted alias: every sibling
// platform-touching command (`gate list`, `platform whoami`, ...) accepts
// --erun-alias to target one plane, but this report had no way to avoid
// probing every configured one even when the caller only cares about a
// single plane. An alias that is not configured, or that names a
// non-erun-hosted provider, is an error rather than a silently empty report.
func ResolveControlPlaneVersionDrift(ctx Context, result ListResult, alias string, deps CloudDependencies, resolvePublished RuntimeRegistryVersionResolverFunc) (ControlPlaneVersionDrift, error) {
	deps = normalizeCloudDependencies(deps)
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
	publishedSemver, publishedOK := parseRegistryStableVersion(drift.PublishedVersion)

	byIdentity := map[string]int{}
	for _, provider := range planes {
		resolveOneControlPlaneVersionStatus(ctx, &drift, byIdentity, provider, deps.FetchPlatformInfo, deps.FetchVersionJSON, deps.ResolveHostAddrs, publishedSemver, publishedOK)
	}
	return drift, nil
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

// selectControlPlaneProviders is controlPlaneProviders narrowed to a single
// alias when one is given, matching the error text ResolveCloudProvider and
// ResolveERunPlatformAlias already use elsewhere so an unconfigured or
// wrong-type --erun-alias reads the same across every command that accepts
// it.
func selectControlPlaneProviders(providers []CloudProviderStatus, alias string) ([]CloudProviderStatus, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return controlPlaneProviders(providers), nil
	}
	for _, provider := range providers {
		if provider.Alias != alias {
			continue
		}
		if provider.Provider != CloudProviderERun {
			return nil, fmt.Errorf("cloud provider alias %q is a %q-type alias, not an erun platform alias", provider.Alias, provider.Provider)
		}
		return []CloudProviderStatus{provider}, nil
	}
	return nil, fmt.Errorf("cloud provider alias %q is not configured", alias)
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
		ctx.Trace("list: would also probe " + provider.Alias + "'s console and docs site at /version.json, using the consoleUrl and docsUrl that GET /v1/platform discloses")
	}
}

// resolveOneControlPlaneVersionStatus fetches one alias's GET /v1/platform
// and either merges it into an already-seen backend's plane entry (see
// controlPlaneBackendIdentity) or appends a new one. The identity check runs
// before the console is ever probed, so a duplicate alias costs one wasted
// GET /v1/platform, not a second GET /version.json for a console already
// checked under the first alias.
func resolveOneControlPlaneVersionStatus(ctx Context, drift *ControlPlaneVersionDrift, byIdentity map[string]int, provider CloudProviderStatus, fetchPlatformInfo func(Context, string) (PlatformInfo, error), fetchVersionJSON func(Context, string) (string, error), resolveHostAddrs func(Context, string) ([]string, error), publishedSemver semver, publishedOK bool) {
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
// same published release.
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

// detectAdvertisedAPIURLMismatch answers the question a discovery document
// naming the wrong apiUrl is actually about: does this plane's own GET
// /v1/platform name a backend erun did not just reach? A textually different
// apiUrl is common and benign -- a vanity hostname CNAMEing to the one erun
// queried -- so this flags only the case that difference cannot explain: the
// discovered hostname resolving to no address this alias's own host shares.
// A host that does not resolve on either side means no verdict rather than a
// guess, the same way an unreadable version is never reported as current.
//
// The two traces below name the host that did not resolve but deliberately not
// the resolver's own error text: that text embeds the machine's resolver
// address ("lookup <host> on 127.0.0.53:53: no such host"), which no golden
// placeholder can stabilise across hosts, and the actionable part is which
// hostname erun could not resolve, not how the local resolver phrased it.
func detectAdvertisedAPIURLMismatch(ctx Context, alias, ownAPIURL, discoveredAPIURL string, resolveHostAddrs func(Context, string) ([]string, error)) string {
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

// resolveControlPlaneEndpoints resolves an api url's host to its address:port
// pairs -- what a mismatch check compares, since a benign canonical alias
// shares these with the hostname erun dialed even though the two names differ.
func resolveControlPlaneEndpoints(ctx Context, apiURL string, resolveHostAddrs func(Context, string) ([]string, error)) ([]string, error) {
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

// defaultResolveHostAddrs is the real DNS lookup behind CloudDependencies'
// ResolveHostAddrs. It ignores ctx.DryRun deliberately:
// ResolveControlPlaneVersionDrift returns before probing any plane under
// --dry-run, so there is no dry-run contract for it to honor here.
func defaultResolveHostAddrs(_ Context, host string) ([]string, error) {
	return net.DefaultResolver.LookupHost(context.Background(), host)
}

// The two labels a surface is reported and traced under. Console and docs site
// answer the same question the same way; only the discovery field they were
// resolved from and the word an operator reads differ.
const (
	versionSurfaceConsole = "console"
	versionSurfaceDocs    = "docs site"
)

func resolveVersionSurfaceStatus(ctx Context, alias, surface, surfaceURL string, fetchVersionJSON func(Context, string) (string, error), publishedSemver semver, publishedOK bool) VersionSurfaceStatus {
	status := VersionSurfaceStatus{URL: surfaceURL}
	ctx.Trace("list: GET " + surfaceURL + "/version.json (" + surface + " version check for " + alias + ")")
	version, err := fetchVersionJSON(ctx, surfaceURL)
	if err != nil {
		var contentErr *versionSurfaceUnexpectedContentError
		if errors.As(err, &contentErr) {
			// The surface answered -- reporting it unreachable would send an
			// operator toward DNS/ingress/TLS when the real fault is the
			// surface serving the wrong document (root AGENTS.md's "Advice
			// that cannot work" dead end).
			status.Reachable = true
			status.Version = "unknown"
			status.Reason = contentErr.Error()
			ctx.Trace("list: " + surface + " for " + alias + " reachable but did not serve the expected document: " + contentErr.Error())
			return status
		}
		// The surface is named on the transport failure the same way the trace
		// line above names it: a plane with both a console and a docs site
		// would otherwise report one bare "unreachable" an operator cannot
		// attribute to either.
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

// versionVerdict compares a deployed version against the published baseline,
// the shared logic behind both ControlPlaneVersionStatus and
// VersionSurfaceStatus's Behind/Ahead fields.
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

// versionSurfaceUnexpectedContentError reports that a surface answered GET
// /version.json but did not serve the expected JSON document -- most
// commonly an SPA's index.html, served by a wildcard nginx fallback for a
// route the deployed config doesn't yet exact-match. It is deliberately a
// distinct type from a transport error: the surface did answer, so a
// caller must never treat this as unreachable.
type versionSurfaceUnexpectedContentError struct {
	statusCode  int
	contentType string
}

func (e *versionSurfaceUnexpectedContentError) Error() string {
	contentType := strings.TrimSpace(e.contentType)
	if contentType == "" {
		contentType = "(no content-type)"
	}
	return fmt.Sprintf("/version.json returned %d %s (expected application/json)", e.statusCode, contentType)
}

// defaultFetchVersionJSON resolves a deployed surface's own build version via
// its unauthenticated GET /version.json -- the console's and the docs site's
// counterpart to defaultFetchPlatformInfo's GET /v1/platform. It makes the
// request directly rather than through fetchJSON so it can tell a transport
// failure (connection refused, DNS, TLS, timeout -- the request never got an
// answer) apart from a content failure (an answer arrived, but not the
// expected JSON document): the two point an operator at completely different
// first moves, and collapsing them into one "unreachable" verdict misdirects.
func defaultFetchVersionJSON(ctx Context, surfaceURL string) (string, error) {
	target := strings.TrimRight(strings.TrimSpace(surfaceURL), "/") + "/version.json"
	ctx.Trace("GET " + target)
	if ctx.DryRun {
		return "", nil
	}
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: erunHTTPTimeout}).Do(req)
	if err != nil {
		// Returned unwrapped: the caller knows which surface it asked about
		// and is the one that says so, in the same words it traces.
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var body struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(respBody, &body); err != nil {
		return "", &versionSurfaceUnexpectedContentError{statusCode: resp.StatusCode, contentType: resp.Header.Get("Content-Type")}
	}
	return body.Version, nil
}

func valueOrNoneLabel(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(none)"
	}
	return value
}
