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

// RuntimeImageChecker reports whether a runtime image is confirmed missing, so
// deploy can bootstrap on the canonical image instead of ImagePullBackOff.
type RuntimeImageChecker interface {
	// ConfirmedMissing is true only on an affirmative registry miss. control is
	// a known-present image probed with the same credential, since a private
	// namespace answers 404 for "not yours" too. Inconclusive reports false.
	ConfirmedMissing(ctx context.Context, image, control string) (bool, error)
	// ConfirmedPresent is true only on an affirmative hit, with the same
	// control-probe precondition and fail-open posture.
	ConfirmedPresent(ctx context.Context, image, control string) (bool, error)
}

const ghcrHost = "ghcr.io"

// Vars so tests can point them at an httptest server.
var (
	ghcrAPIBase   = "https://" + ghcrHost
	ghcrTokenBase = "https://" + ghcrHost + "/token"
)

var errProbeInconclusive = errors.New("registry probe inconclusive")

// GHCRImageChecker probes ghcr.io with the deploy Job's pull credential; an
// anonymous caller cannot tell a missing image from a private one.
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

// ConfirmedMissing never confirms hosts other than ghcr.io.
func (c *GHCRImageChecker) ConfirmedMissing(ctx context.Context, image, control string) (bool, error) {
	status, ok := c.probeManifestStatus(ctx, image, control)
	if !ok {
		return false, nil
	}
	return status == http.StatusNotFound, nil
}

// ConfirmedPresent never confirms hosts other than ghcr.io.
func (c *GHCRImageChecker) ConfirmedPresent(ctx context.Context, image, control string) (bool, error) {
	status, ok := c.probeManifestStatus(ctx, image, control)
	if !ok {
		return false, nil
	}
	return status == http.StatusOK, nil
}

// probeManifestStatus returns ok=false when undecidable: an unparseable or
// foreign-host reference, or a control probe that did not return 200.
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

// pullToken authenticates with basic auth; ghcr.io refuses to answer anonymously
// for private or nonexistent repositories.
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

// parseImageReference requires both a host segment and a tag.
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
