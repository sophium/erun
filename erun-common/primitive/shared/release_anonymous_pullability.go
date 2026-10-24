package shared

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/infra/registry"
)

// anonymousManifestAcceptHeaders mirrors what `docker manifest inspect` sends.
const anonymousManifestAcceptHeaders = "application/vnd.oci.image.index.v1+json," +
	"application/vnd.docker.distribution.manifest.list.v2+json," +
	"application/vnd.oci.image.manifest.v1+json," +
	"application/vnd.docker.distribution.manifest.v2+json"

const anonymousPullProbeTimeout = 15 * time.Second

// AnonymousPullProbeFunc reports whether repoPath:tag pulls with no credential; injectable for tests.
type AnonymousPullProbeFunc func(ctx context.Context, client *http.Client, repoPath, tag string) (bool, error)

// ProbeAnonymousManifestPull is the live ghcr.io AnonymousPullProbeFunc.
func ProbeAnonymousManifestPull(ctx context.Context, client *http.Client, repoPath, tag string) (bool, error) {
	if override, ok := os.LookupEnv(anonymousPullableOverrideEnv); ok {
		return anonymousPullableOverrideHasImage(override, repoPath, tag), nil
	}
	return ProbeAnonymousManifestPullAt(ctx, client, repoPath, tag, "", "")
}

// anonymousPullableOverrideEnv is a test-only list of "<repoPath>:<tag>" entries treated as
// pullable; anything absent is not. Unset means the live probe runs.
const anonymousPullableOverrideEnv = "ERUN_ANONYMOUS_PULLABLE_OVERRIDE"

func anonymousPullableOverrideHasImage(override, repoPath, tag string) bool {
	for _, entry := range strings.Split(override, ",") {
		wantPath, wantTag, ok := CutLast(strings.TrimSpace(entry), ":")
		if !ok {
			continue
		}
		if strings.TrimSpace(wantPath) == repoPath && strings.TrimSpace(wantTag) == tag {
			return true
		}
	}
	return false
}

// ProbeAnonymousManifestPullAt takes explicit endpoints. hasAuth is always false: the
// authenticated verify cannot see whether a stranger can pull.
func ProbeAnonymousManifestPullAt(ctx context.Context, client *http.Client, repoPath, tag, baseURL, tokenURL string) (bool, error) {
	if client == nil {
		client = &http.Client{Timeout: anonymousPullProbeTimeout}
	}
	baseURL = registry.NormalizeGHCRBaseURL(baseURL)
	tokenURL = registry.NormalizeGHCRBaseURL(tokenURL)
	repoPath = strings.ToLower(repoPath)

	// A 401/403 is a conclusive "not pullable"; any other failure is inconclusive and fails the release.
	token, err := registry.FetchGHCRPullToken(ctx, client, repoPath, tokenURL, registry.RegistryBasicAuth{}, false)
	if err != nil {
		if errors.Is(err, registry.ErrRegistryAuthRequired) {
			return false, nil
		}
		return false, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v2/"+repoPath+"/manifests/"+tag, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", anonymousManifestAcceptHeaders)
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, nil
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return false, nil
	}
	return false, registry.RegistryStatusError("anonymous manifest pull", resp.Status, resp.StatusCode)
}
