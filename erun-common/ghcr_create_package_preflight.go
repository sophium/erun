package eruncommon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// A release that introduces a new component discovers it cannot create that
// component's package only at the push, after every image has been built for
// every architecture. VerifyGHCRPushScope (ghcr_push_preflight.go) already
// checks the credential's write:packages scope up front, but that scope does
// not by itself grant the right to CREATE a package that has never existed —
// an org can restrict creation to its owner even for a token that pushes
// every existing package fine. Whether creation is allowed is not knowable
// from the token's scopes; it is knowable from the registry itself, the same
// way `docker push`/`helm push` learn it: by asking for a token scoped to push
// the specific repository and starting (never finishing) a blob upload. That
// costs one small HTTP exchange and uploads nothing, so it is cheap enough to
// run before the build spend for every resolved image and chart.

// ghcrCreatePackageProbeTimeout bounds each registry round trip this preflight
// makes, so an unreachable registry cannot hang a release; it makes the check
// inconclusive instead.
const ghcrCreatePackageProbeTimeout = 10 * time.Second

// MissingGHCRCreatePackageError is returned when the registry has already told
// erun a resolved artifact cannot be pushed, whether because its package does
// not exist yet and this credential cannot create one, or for any other reason
// the registry denies push — the registry's own denial is preserved verbatim
// so the reader sees exactly what a real push would have hit.
type MissingGHCRCreatePackageError struct {
	Registry   string
	Repository string
	Detail     string
}

func (e *MissingGHCRCreatePackageError) Error() string {
	detail := strings.TrimSpace(e.Detail)
	if detail == "" {
		detail = "the registry denied push access"
	}
	return fmt.Sprintf(
		"the credential erun would push %s/%s with cannot push to it: %s\n"+
			"Publishing is checked up front because a release that cannot push a new package would "+
			"otherwise fail at the push, after building every image for every architecture.\n\n"+
			"If this is the first publish of a new component, its package does not exist under this "+
			"namespace yet, and creating one for the first time needs the namespace owner's classic PAT:\n"+
			"  1. Sign into github.com as the account that owns the namespace.\n"+
			"  2. Open https://github.com/settings/tokens/new (classic).\n"+
			"  3. Generate a token with scopes: write:packages and read:packages.\n"+
			"  4. docker logout %s\n"+
			"  5. echo $TOKEN | docker login %s -u <owner> --password-stdin\n"+
			"  6. Re-run erun release (or erun push).\n"+
			"After the package exists once, the owner can grant Write access to others (per-package "+
			"settings, or \"Inherit access from source repository\" on a linked repo); future versions "+
			"can then be pushed by anyone with that access.",
		e.Registry, e.Repository, detail, e.Registry, e.Registry)
}

// VerifyGHCRCanPushImage is the image-tag entry point: it answers only for
// ghcr, and only when it can answer with certainty. An inconclusive check —
// no credential resolved, the registry unreachable, an unexpected response —
// returns nil rather than blocking a build on a guess; a credential that is
// actually bad still fails at the real push exactly as it does today.
func VerifyGHCRCanPushImage(ctx context.Context, client *http.Client, tag string) error {
	registry := dockerRegistryFromImageTag(tag)
	if !isGHCRRegistry(registry) {
		return nil
	}
	repository := dockerRepositoryFromTag(tag)
	if repository == "" {
		return nil
	}
	auth, ok := resolveGHCRBasicAuth(DockerNamespaceFromTag(tag))
	if !ok {
		return nil
	}
	return verifyGHCRCanPushRepositoryFor(ctx, client, registry, repository, auth, "https://"+registry)
}

// VerifyGHCRCanPushChart is the chart entry point: ociRepo is the
// `oci://<registry>/<path>` a chart publishes under and chartName is the
// segment that names the specific chart within it.
func VerifyGHCRCanPushChart(ctx context.Context, client *http.Client, ociRepo, chartName string) error {
	registry, path := splitOCIChartRepo(ociRepo)
	if !isGHCRRegistry(registry) || strings.TrimSpace(chartName) == "" {
		return nil
	}
	repository := strings.TrimPrefix(strings.TrimSuffix(path, "/")+"/"+strings.TrimSpace(chartName), "/")
	auth, ok := resolveGHCRBasicAuth(namespaceFromOCIPath(path))
	if !ok {
		return nil
	}
	return verifyGHCRCanPushRepositoryFor(ctx, client, registry, repository, auth, "https://"+registry)
}

// dockerRepositoryFromTag returns the repository path a registry addresses —
// everything between the registry host and the tag — or "" when tag names no
// separate registry host (a bare name docker would resolve against Docker
// Hub, which this preflight does not apply to).
func dockerRepositoryFromTag(tag string) string {
	tag = strings.TrimSpace(tag)
	registry := dockerRegistryFromImageTag(tag)
	if registry == "" {
		return ""
	}
	rest := strings.TrimPrefix(tag, registry+"/")
	if rest == tag {
		return ""
	}
	if cut := strings.LastIndexByte(rest, ':'); cut >= 0 {
		rest = rest[:cut]
	}
	return rest
}

// splitOCIChartRepo splits `oci://<registry>/<path>` into its host and path.
func splitOCIChartRepo(ociRepo string) (string, string) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(ociRepo), "oci://")
	registry, path, ok := strings.Cut(trimmed, "/")
	if !ok {
		return trimmed, ""
	}
	return registry, path
}

// namespaceFromOCIPath returns the first path segment, which is the owning
// namespace for a chart OCI repo the same way it is for a docker image tag.
func namespaceFromOCIPath(path string) string {
	segment, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	return segment
}

// verifyGHCRCanPushRepositoryFor is the decision, with the credential and the
// registry API base URL as explicit inputs so it can be exercised without a
// real credential or a live registry.
func verifyGHCRCanPushRepositoryFor(ctx context.Context, client *http.Client, registry, repository string, auth registryBasicAuth, baseURL string) error {
	if client == nil {
		client = &http.Client{Timeout: ghcrCreatePackageProbeTimeout}
	}
	token, ok := ghcrRepositoryPushToken(ctx, client, registry, repository, auth, baseURL)
	if !ok {
		return nil
	}
	allowed, detail, conclusive := ghcrProbeBlobUploadSession(ctx, client, repository, token, baseURL)
	if !conclusive || allowed {
		return nil
	}
	return &MissingGHCRCreatePackageError{Registry: registry, Repository: repository, Detail: detail}
}

// ghcrRepositoryPushToken exchanges the resolved credential for a registry
// token scoped to push this one repository — the same exchange a real
// `docker push`/`helm push` performs before its first request. Getting a
// token back is not yet proof of anything; ghcrProbeBlobUploadSession is what
// tells push-denied apart from push-granted.
func ghcrRepositoryPushToken(ctx context.Context, client *http.Client, registry, repository string, auth registryBasicAuth, baseURL string) (string, bool) {
	tokenURL := fmt.Sprintf("%s/token?service=%s&scope=repository:%s:pull,push", baseURL, registry, repository)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	if err != nil {
		return "", false
	}
	req.SetBasicAuth(auth.username, auth.secret)
	resp, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", false
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", false
	}
	if strings.TrimSpace(payload.Token) == "" {
		return "", false
	}
	return payload.Token, true
}

// ghcrProbeBlobUploadSession starts a blob upload — the first request any
// docker/helm push makes — and never finishes it. The registry allocates the
// session exactly when push is allowed, including creating the repository for
// the first time, so a 202/201 is conclusive proof push would have succeeded;
// a 401/403 is the registry's own denial, conclusive the other way, with the
// same detail (e.g. create_package) a real push would have surfaced. Any
// other outcome — network failure, an unexpected status — is inconclusive:
// this preflight only ever blocks on a definite answer.
func ghcrProbeBlobUploadSession(ctx context.Context, client *http.Client, repository, token, baseURL string) (allowed bool, detail string, conclusive bool) {
	uploadURL := fmt.Sprintf("%s/v2/%s/blobs/uploads/", baseURL, repository)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, nil)
	if err != nil {
		return false, "", false
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.ContentLength = 0
	resp, err := client.Do(req)
	if err != nil {
		return false, "", false
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusAccepted, http.StatusCreated:
		abandonGHCRBlobUploadSession(ctx, client, resp.Header.Get("Location"), token, baseURL)
		return true, "", true
	case http.StatusUnauthorized, http.StatusForbidden:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return false, strings.TrimSpace(string(body)), true
	default:
		return false, "", false
	}
}

// abandonGHCRBlobUploadSession cancels the probe's own upload session so
// nothing about it lingers in the registry's view of in-progress uploads.
// Best effort: a session the registry never sees a DELETE for is garbage
// collected on its own, so a failure here is not worth failing the release
// over.
func abandonGHCRBlobUploadSession(ctx context.Context, client *http.Client, location, token, baseURL string) {
	location = strings.TrimSpace(location)
	if location == "" {
		return
	}
	cancelURL := location
	if !strings.HasPrefix(cancelURL, "http") {
		cancelURL = baseURL + location
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, cancelURL, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}
