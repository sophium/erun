package eruncommon

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// moduleReferencedImagePattern matches an erun image reference a Terraform
// module names by repository shape (ghcr.io/sophium/erun-<component>:),
// independent of what follows the colon. The cluster-edge module's
// dns01_webhook_image resolves its version from a Terraform interpolation
// (${local.dns01_webhook_chart_app_version}) rather than a literal, so a
// pattern anchored on a literal version -- like pin.go's own
// erunImageReferencePattern -- would never see it; this one only needs the
// repository name, which is enough to know what a module references.
var moduleReferencedImagePattern = regexp.MustCompile(`ghcr\.io/sophium/(erun-[a-zA-Z0-9_-]+):`)

// discoverModuleReferencedImageNames scans every Terraform file under root for
// an erun image a published module references, so the release-time
// anonymous-pullability check tracks whatever a module actually names instead
// of a hand-maintained list that can silently fall behind. A root that does
// not exist (a tenant's own release, or a checkout with no
// erun-devops/terraform-erun of its own) yields no names rather than an
// error: this check only ever has something to do for erun's own release.
func discoverModuleReferencedImageNames(root string) ([]string, error) {
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	seen := map[string]struct{}{}
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".tf") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, match := range moduleReferencedImagePattern.FindAllStringSubmatch(string(data), -1) {
			seen[match[1]] = struct{}{}
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// anonymousPullabilityBaseline is a shrink-only baseline -- the same shape as
// KnownUnsurfacedRoutes (erun-backend-api/internal/routes/route_audit.go) and
// issueReferenceBaseline (erun-integration/issue_reference_test.go) -- for an
// erun image a Terraform module references that is known not to be
// anonymously pullable yet. It is a record of a known gap, not a design
// decision: an entry here means the ghcr.io package is currently private, a
// visibility setting on the sophium org this repository cannot flip on its
// own, not that the image is meant to stay private. Once the package is made
// public, TestAnonymousPullabilityBaselineIsCurrent fails until the stale
// entry is removed -- the same shrink-only enforcement the two precedents
// above use -- which is what proves the visibility fix actually landed rather
// than being asserted in a PR description.
var anonymousPullabilityBaseline = map[string]bool{
	// ghcr.io/sophium/erun-dns01-webhook is private, so an anonymous pull
	// fails; terraform-erun-cluster-edge's dns01_webhook_image references it
	// directly, which is what makes a cluster following that module's
	// documented powerdns-broker path end at ImagePullBackOff.
	"erun-dns01-webhook": true,
}

// anonymousManifestAcceptHeaders is every manifest media type a multi-arch
// pull negotiates, mirroring what `docker manifest inspect` itself sends.
const anonymousManifestAcceptHeaders = "application/vnd.oci.image.index.v1+json," +
	"application/vnd.docker.distribution.manifest.list.v2+json," +
	"application/vnd.oci.image.manifest.v1+json," +
	"application/vnd.docker.distribution.manifest.v2+json"

// anonymousPullProbeTimeout bounds each registry round trip this check makes,
// so an unreachable registry makes the release fail fast rather than hang.
const anonymousPullProbeTimeout = 15 * time.Second

// anonymousPullProbeFunc resolves whether repoPath's tag can be pulled with no
// credential at all. A function type rather than a bare call so a test can
// inject a fake instead of reaching a live registry (erun-common/AGENTS.md's
// "prefer dependency injection in tests instead of replacing globals").
type anonymousPullProbeFunc func(ctx context.Context, client *http.Client, repoPath, tag string) (bool, error)

// probeAnonymousManifestPull is the real anonymousPullProbeFunc, against the
// live ghcr.io endpoints.
func probeAnonymousManifestPull(ctx context.Context, client *http.Client, repoPath, tag string) (bool, error) {
	return probeAnonymousManifestPullAt(ctx, client, repoPath, tag, "", "")
}

// probeAnonymousManifestPullAt is probeAnonymousManifestPull with the registry
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
func probeAnonymousManifestPullAt(ctx context.Context, client *http.Client, repoPath, tag, baseURL, tokenURL string) (bool, error) {
	if client == nil {
		client = &http.Client{Timeout: anonymousPullProbeTimeout}
	}
	baseURL = normalizeGHCRBaseURL(baseURL)
	tokenURL = normalizeGHCRBaseURL(tokenURL)
	repoPath = strings.ToLower(repoPath)

	token, err := fetchGHCRPullToken(ctx, client, repoPath, tokenURL, registryBasicAuth{}, false)
	if err != nil {
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
	return resp.StatusCode >= 200 && resp.StatusCode < 300, nil
}

// verifyModuleReferencedImagesAnonymouslyPullable asserts that every erun
// image a Terraform module references resolves with no credential at all, at
// the version this release just published. A name absent from
// anonymousPullabilityBaseline must resolve anonymously or the release fails;
// a baselined name is reported and let through, so today's known-private
// erun-dns01-webhook does not block every release until the sophium org's
// package visibility changes.
func verifyModuleReferencedImagesAnonymouslyPullable(ctx Context, projectRoot string, execution BuildExecutionSpec, probe anonymousPullProbeFunc) error {
	names, err := discoverModuleReferencedImageNames(filepath.Join(strings.TrimSpace(projectRoot), "erun-devops", "terraform-erun"))
	if err != nil {
		return fmt.Errorf("scan terraform modules for image references: %w", err)
	}
	if len(names) == 0 {
		return nil
	}

	byName := make(map[string]DockerImageReference, len(execution.dockerPushes))
	for _, pushInput := range execution.dockerPushes {
		byName[pushInput.Image.ImageName] = pushInput.Image
	}

	for _, name := range names {
		image, ok := byName[name]
		if !ok {
			return fmt.Errorf("a terraform module references image %s but this release does not publish it -- anonymous pullability cannot be asserted for an image nothing publishes", name)
		}
		if err := verifyImageAnonymouslyPullable(ctx, name, image, probe); err != nil {
			return err
		}
	}
	return nil
}

// verifyImageAnonymouslyPullable is the per-image half of
// verifyModuleReferencedImagesAnonymouslyPullable: probe name's tag with no
// credential at all, then apply anonymousPullabilityBaseline to the result.
func verifyImageAnonymouslyPullable(ctx Context, name string, image DockerImageReference, probe anonymousPullProbeFunc) error {
	tag := strings.TrimSpace(image.Tag)
	if !isGHCRRegistry(dockerRegistryFromImageTag(tag)) {
		return nil
	}
	ctx.Trace("release: anonymous-pullability probing " + tag + " with a fresh, credential-free token")
	if ctx.DryRun {
		return nil
	}
	repoPath := DockerNamespaceFromTag(tag) + "/" + name
	pullable, probeErr := probe(context.Background(), nil, repoPath, image.Version)
	if probeErr != nil {
		return fmt.Errorf("probe anonymous pull of %s: %w", tag, probeErr)
	}
	if pullable {
		ctx.Info("==> Verified anonymously pullable: " + tag)
		return nil
	}
	if anonymousPullabilityBaseline[name] {
		ctx.Info("==> Not anonymously pullable (baselined, see anonymousPullabilityBaseline): " + tag)
		return nil
	}
	return fmt.Errorf(
		"image %s resolves for the publishing credential but not anonymously -- a cluster with no pull secret for it cannot pull this image.\n"+
			"Either make the ghcr.io/sophium/%s package public, or add %q to anonymousPullabilityBaseline in release_anonymous_pullability.go with a comment explaining why",
		tag, name, name)
}
