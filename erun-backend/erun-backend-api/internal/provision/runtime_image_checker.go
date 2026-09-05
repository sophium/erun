package provision

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// RuntimeImageChecker reports whether an environment's runtime image is known
// to be missing from its registry, so create/deploy can bootstrap on the
// canonical published erun-devops image instead of starting a Job that can
// only ImagePullBackOff on an image nobody ever published. A tenant that never
// published a `<tenant>-devops` image (internal/provision/environments.go's
// deployJobParams names the image every hosted deploy pulls) is the
// precondition this exists to catch.
type RuntimeImageChecker interface {
	// ConfirmedMissing reports true only when the registry affirmatively
	// confirms the tag is absent. control names a reference in the same
	// registry that must exist — the canonical image the bootstrap would run —
	// and is probed with the same credential, because "absent" only means
	// absent when the probe that said so can still see something that is
	// there: a private namespace answers an unauthenticated (or under-scoped)
	// caller identically whether or not the repository exists. Any other
	// outcome — no usable credential, a network error, a host this checker
	// cannot query — reports false: this is a best-effort catch of the
	// *knowable* failure, not a gate that blocks a deploy whenever a registry
	// probe is inconclusive.
	ConfirmedMissing(ctx context.Context, image, control string) (bool, error)
	// ConfirmedPresent reports true only when the registry affirmatively
	// confirms the tag resolves, under the same control-probe precondition as
	// ConfirmedMissing. It is what lets a caller distinguish "the fallback is
	// masking an image the platform actually published under another name"
	// from "nothing is known" — absent, forbidden, and inconclusive all
	// report false, the same fail-open posture as ConfirmedMissing.
	ConfirmedPresent(ctx context.Context, image, control string) (bool, error)
}

// ghcrHost is the only registry this checker knows how to interrogate.
const ghcrHost = "ghcr.io"

// ghcrAPIBase and ghcrTokenBase are declared as vars so tests can point them at
// an httptest server instead of the real ghcr.io.
var (
	ghcrAPIBase   = "https://" + ghcrHost
	ghcrTokenBase = "https://" + ghcrHost + "/token"
)

// errProbeInconclusive marks every outcome that leaves the question open, so a
// caller can only ever act on a status the registry actually returned.
var errProbeInconclusive = errors.New("registry probe inconclusive")

// GHCRImageChecker probes a ghcr.io-hosted image with the registry pull-token
// flow, authenticated with the same pull credential the deploy Job's own
// ServiceAccount carries. The credential is what makes the probe decisive: an
// anonymous caller cannot even obtain a pull token for a private namespace, so
// it can never tell a missing image from one it is not allowed to see, and a
// private namespace is the normal case for a tenant's runtime image.
type GHCRImageChecker struct {
	Client      *http.Client
	Credentials RegistryCredentials
}

func NewGHCRImageChecker(credentials RegistryCredentials) *GHCRImageChecker {
	return &GHCRImageChecker{Client: &http.Client{Timeout: 10 * time.Second}, Credentials: credentials}
}

func (c *GHCRImageChecker) httpClient() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return http.DefaultClient
}

// ConfirmedMissing implements RuntimeImageChecker for `<host>/<repo>:<tag>`
// references. Hosts other than ghcr.io (a private mirror, a self-hosted
// registry) are not this checker's responsibility and are never confirmed.
func (c *GHCRImageChecker) ConfirmedMissing(ctx context.Context, image, control string) (bool, error) {
	status, ok := c.probeManifestStatus(ctx, image, control)
	if !ok {
		return false, nil
	}
	return status == http.StatusNotFound, nil
}

// ConfirmedPresent implements RuntimeImageChecker's positive counterpart to
// ConfirmedMissing, sharing the same control-probe precondition and the same
// ghcr.io-only scope.
func (c *GHCRImageChecker) ConfirmedPresent(ctx context.Context, image, control string) (bool, error) {
	status, ok := c.probeManifestStatus(ctx, image, control)
	if !ok {
		return false, nil
	}
	return status == http.StatusOK, nil
}

// probeManifestStatus resolves image's manifest status, or ok=false when the
// question is not decidable at all: an unparseable/foreign-host reference, or
// a control probe that itself did not come back 200. The control probe proves
// the credential can read this registry at all — without it a 404 is
// indistinguishable from "you may not look", which is what made this check
// unreachable against a private namespace.
func (c *GHCRImageChecker) probeManifestStatus(ctx context.Context, image, control string) (int, bool) {
	host, repo, tag, ok := parseImageReference(image)
	if !ok || host != ghcrHost {
		return 0, false
	}
	controlHost, controlRepo, controlTag, ok := parseImageReference(control)
	if !ok || controlHost != host {
		return 0, false
	}
	credential, _ := c.credentialFor(ctx, host)
	if status, err := c.manifestStatus(ctx, controlRepo, controlTag, credential); err != nil || status != http.StatusOK {
		return 0, false
	}
	status, err := c.manifestStatus(ctx, repo, tag, credential)
	if err != nil {
		return 0, false
	}
	return status, true
}

// manifestStatus returns the registry's own status for one manifest request, or
// errProbeInconclusive when the exchange never got far enough to produce one.
func (c *GHCRImageChecker) manifestStatus(ctx context.Context, repo, tag string, credential RegistryCredential) (int, error) {
	token, err := c.pullToken(ctx, repo, credential)
	if err != nil || strings.TrimSpace(token) == "" {
		return 0, errProbeInconclusive
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, ghcrAPIBase+"/v2/"+repo+"/manifests/"+tag, nil)
	if err != nil {
		return 0, errProbeInconclusive
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return 0, errProbeInconclusive
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, nil
}

// pullToken exchanges the registry credential for a repository-scoped pull
// token. Presenting the credential as basic auth is what lets the exchange
// succeed for a private repository — and what makes the registry answer for a
// repository that does not exist at all, which it refuses to do anonymously.
func (c *GHCRImageChecker) pullToken(ctx context.Context, repo string, credential RegistryCredential) (string, error) {
	tokenURL := ghcrTokenBase + "?scope=" + url.QueryEscape("repository:"+repo+":pull") + "&service=" + ghcrHost
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	if err != nil {
		return "", err
	}
	if credential.Usable() {
		req.SetBasicAuth(credential.Username, credential.Password)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", nil
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.Token, nil
}

func (c *GHCRImageChecker) credentialFor(ctx context.Context, host string) (RegistryCredential, bool) {
	if c.Credentials == nil {
		return RegistryCredential{}, false
	}
	return c.Credentials.For(ctx, host)
}

// parseImageReference splits a `<host>/<repo>:<tag>` image reference. It
// rejects anything without both a host segment and a tag, which is every
// image deployJobParams ever constructs.
func parseImageReference(image string) (host, repo, tag string, ok bool) {
	image = strings.TrimSpace(image)
	slash := strings.Index(image, "/")
	if slash < 0 {
		return "", "", "", false
	}
	host = image[:slash]
	rest := image[slash+1:]
	colon := strings.LastIndex(rest, ":")
	if colon < 0 {
		return "", "", "", false
	}
	repo = rest[:colon]
	tag = rest[colon+1:]
	if repo == "" || tag == "" {
		return "", "", "", false
	}
	return host, repo, tag, true
}
