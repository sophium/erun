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

// anonymousManifestAcceptHeaders is every manifest media type a multi-arch
// pull negotiates, mirroring what `docker manifest inspect` itself sends.
const anonymousManifestAcceptHeaders = "application/vnd.oci.image.index.v1+json," +
	"application/vnd.docker.distribution.manifest.list.v2+json," +
	"application/vnd.oci.image.manifest.v1+json," +
	"application/vnd.docker.distribution.manifest.v2+json"

// anonymousPullProbeTimeout bounds each registry round trip this check makes,
// so an unreachable registry makes the release fail fast rather than hang.
const anonymousPullProbeTimeout = 15 * time.Second

// AnonymousPullProbeFunc resolves whether repoPath's tag can be pulled with no
// credential at all. A function type rather than a bare call so a test can
// inject a fake instead of reaching a live registry (erun-common/AGENTS.md's
// "prefer dependency injection in tests instead of replacing globals").
type AnonymousPullProbeFunc func(ctx context.Context, client *http.Client, repoPath, tag string) (bool, error)

// ProbeAnonymousManifestPull is the real anonymousPullProbeFunc, against the
// live ghcr.io endpoints.
func ProbeAnonymousManifestPull(ctx context.Context, client *http.Client, repoPath, tag string) (bool, error) {
	if override, ok := os.LookupEnv(anonymousPullableOverrideEnv); ok {
		return anonymousPullableOverrideHasImage(override, repoPath, tag), nil
	}
	return ProbeAnonymousManifestPullAt(ctx, client, repoPath, tag, "", "")
}

// anonymousPullableOverrideEnv is a test-only seam that answers "is
// <repoPath>:<tag> anonymously pullable?" from a static list instead of a
// live ghcr.io read, the same shape publishedChartProbeOverrideEnv gives the
// runtime chart search: an integration golden must never depend on a real
// registry's actual visibility setting. Not a production knob: when the
// variable is unset, the real network probe runs. Format: comma-separated
// "<repoPath>:<tag>" entries treated as anonymously pullable; anything absent
// (including an empty value) is treated as not pullable -- the same
// fail-closed default the live probe applies to a definitive 401/403, so a
// scenario that wants the "confirmed private" or "confirmed public" branch
// states it explicitly rather than getting it by omission.
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

// ProbeAnonymousManifestPullAt is probeAnonymousManifestPull with the registry
// and token endpoints as explicit inputs, so it can be exercised against a
// stub server instead of the real ghcr.io.
//
// hasAuth is hard-coded false on the token exchange below -- never
// resolveGHCRBasicAuth or any other docker/gh credential source erun already
// knows how to resolve -- which is what makes this probe genuinely anonymous
// rather than a second run of the check verifyPublishedReleaseArtifacts
// already does. That check re-resolves the same manifest through `docker
// manifest inspect`, which authenticates with whatever the local daemon has
// stored, so it proves only "this resolves for the account that just
// published it". It structurally cannot observe whether a stranger with no
// credential at all can pull the same manifest -- exactly the property
// erun-dns01-webhook's own private package hid from it, three layers away
// from where the failure actually surfaced.
func ProbeAnonymousManifestPullAt(ctx context.Context, client *http.Client, repoPath, tag, baseURL, tokenURL string) (bool, error) {
	if client == nil {
		client = &http.Client{Timeout: anonymousPullProbeTimeout}
	}
	baseURL = registry.NormalizeGHCRBaseURL(baseURL)
	tokenURL = registry.NormalizeGHCRBaseURL(tokenURL)
	repoPath = strings.ToLower(repoPath)

	// A 401/403 here is ghcr answering the anonymous token request with "no" --
	// a conclusive refusal, not a failure to reach the registry -- so it
	// resolves to pullable=false with no error and lets the baseline decide.
	// Any other failure (DNS, connection refused, timeout, a non-2xx status
	// registryStatusError doesn't recognize as an auth refusal) is genuinely
	// inconclusive and must keep failing the release loudly.
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
