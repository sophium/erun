package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// write:packages does not imply the right to create a new package (an org can
// restrict creation), so this preflight asks the registry by opening, and
// abandoning, a blob upload before the build.

// ghcrCreatePackageProbeTimeout makes an unreachable registry inconclusive instead of hanging.
const ghcrCreatePackageProbeTimeout = 10 * time.Second

// MissingGHCRCreatePackageError carries the registry's push denial verbatim.
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
			"  6. Re-run erun build --release (or erun push).\n"+
			"After the package exists once, the owner can grant Write access to others (per-package "+
			"settings, or \"Inherit access from source repository\" on a linked repo); future versions "+
			"can then be pushed by anyone with that access.",
		e.Registry, e.Repository, detail, e.Registry, e.Registry)
}

// VerifyGHCRCanPushImage blocks only on a conclusive denial; anything
// inconclusive returns nil.
func VerifyGHCRCanPushImage(ctx context.Context, client *http.Client, tag string) error {
	registry := DockerRegistryFromImageTag(tag)
	if !IsGHCRRegistry(registry) {
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

// VerifyGHCRCanPushChart checks chartName under the oci://<registry>/<path> ociRepo.
func VerifyGHCRCanPushChart(ctx context.Context, client *http.Client, ociRepo, chartName string) error {
	registry, path := splitOCIChartRepo(ociRepo)
	if !IsGHCRRegistry(registry) || strings.TrimSpace(chartName) == "" {
		return nil
	}
	repository := strings.TrimPrefix(strings.TrimSuffix(path, "/")+"/"+strings.TrimSpace(chartName), "/")
	auth, ok := resolveGHCRBasicAuth(namespaceFromOCIPath(path))
	if !ok {
		return nil
	}
	return verifyGHCRCanPushRepositoryFor(ctx, client, registry, repository, auth, "https://"+registry)
}

// dockerRepositoryFromTag returns the repository path, or "" for a tag with no registry host.
func dockerRepositoryFromTag(tag string) string {
	tag = strings.TrimSpace(tag)
	registry := DockerRegistryFromImageTag(tag)
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

func splitOCIChartRepo(ociRepo string) (string, string) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(ociRepo), "oci://")
	registry, path, ok := strings.Cut(trimmed, "/")
	if !ok {
		return trimmed, ""
	}
	return registry, path
}

// namespaceFromOCIPath returns the owning namespace (first segment).
func namespaceFromOCIPath(path string) string {
	segment, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	return segment
}

func verifyGHCRCanPushRepositoryFor(ctx context.Context, client *http.Client, registry, repository string, auth RegistryBasicAuth, baseURL string) error {
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

// ghcrRepositoryPushToken exchanges the credential for a push-scoped token; that
// alone proves nothing.
func ghcrRepositoryPushToken(ctx context.Context, client *http.Client, registry, repository string, auth RegistryBasicAuth, baseURL string) (string, bool) {
	tokenURL := fmt.Sprintf("%s/token?service=%s&scope=repository:%s:pull,push", baseURL, registry, repository)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	if err != nil {
		return "", false
	}
	req.SetBasicAuth(auth.Username, auth.Secret)
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

// ghcrProbeBlobUploadSession starts a blob upload: 201/202 proves push (including
// package creation), 401/403 is a conclusive denial, anything else is inconclusive.
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

// abandonGHCRBlobUploadSession is best effort; the registry GCs unclosed sessions.
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
