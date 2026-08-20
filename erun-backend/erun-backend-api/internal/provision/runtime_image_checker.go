package provision

import (
	"context"
	"encoding/json"
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
	// Exists reports false only when the registry affirmatively confirms the
	// tag is absent (HTTP 404 from a manifest request the checker could
	// actually make). Any other outcome — network error, auth required, a
	// host this checker does not know how to query — reports true: this is a
	// best-effort catch of the *knowable* failure, not a gate that blocks a
	// deploy whenever a registry probe is inconclusive.
	Exists(ctx context.Context, image string) (bool, error)
}

// ghcrAPIBase and ghcrTokenURL are declared as vars so tests can point them at
// an httptest server instead of the real ghcr.io.
var (
	ghcrAPIBase   = "https://ghcr.io"
	ghcrTokenBase = "https://ghcr.io/token"
)

// GHCRImageChecker probes a ghcr.io-hosted image with the anonymous pull-token
// flow (the same one an unauthenticated `docker pull` uses for a public
// package). It is deliberately conservative: a private package or a host it
// cannot reach reports "cannot verify" (true), never a false negative that
// would block a legitimate deploy.
type GHCRImageChecker struct {
	Client *http.Client
}

func NewGHCRImageChecker() *GHCRImageChecker {
	return &GHCRImageChecker{Client: &http.Client{Timeout: 10 * time.Second}}
}

func (c *GHCRImageChecker) httpClient() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return http.DefaultClient
}

// Exists implements RuntimeImageChecker for `<host>/<repo>:<tag>` references.
// Hosts other than ghcr.io (a private mirror, a self-hosted registry) are not
// this checker's responsibility and always report true.
func (c *GHCRImageChecker) Exists(ctx context.Context, image string) (bool, error) {
	host, repo, tag, ok := parseImageReference(image)
	if !ok || host != "ghcr.io" {
		return true, nil
	}
	token, err := c.anonymousPullToken(ctx, repo)
	if err != nil || strings.TrimSpace(token) == "" {
		return true, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, ghcrAPIBase+"/v2/"+repo+"/manifests/"+tag, nil)
	if err != nil {
		return true, nil
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return true, nil
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode != http.StatusNotFound, nil
}

func (c *GHCRImageChecker) anonymousPullToken(ctx context.Context, repo string) (string, error) {
	tokenURL := ghcrTokenBase + "?scope=" + url.QueryEscape("repository:"+repo+":pull") + "&service=ghcr.io"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	if err != nil {
		return "", err
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
