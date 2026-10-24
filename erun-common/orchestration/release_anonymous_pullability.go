package orchestration

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/helm"
	"github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/shared"
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

// DiscoverModuleReferencedImageNames scans every Terraform file under root for
// an erun image a published module references, so the release-time
// anonymous-pullability check tracks whatever a module actually names instead
// of a hand-maintained list that can silently fall behind. A root that does
// not exist (a tenant's own release, or a checkout with no
// erun-devops/terraform-erun of its own) yields no names rather than an
// error: this check only ever has something to do for erun's own release.
func DiscoverModuleReferencedImageNames(root string) ([]string, error) {
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

// AnonymousPullabilityBaseline is a shrink-only baseline -- the same shape as
// KnownUnsurfacedRoutes (erun-backend-api/internal/routes/route_audit.go) and
// issueReferenceBaseline (erun-integration/issue_reference_test.go) -- for an
// erun image a Terraform module references that is known not to be
// anonymously pullable yet. It is a record of a known gap, not a design
// decision: an entry here means the ghcr.io package is currently private, a
// visibility setting on the sophium org this repository cannot flip on its
// own, not that the image is meant to stay private.
//
// This baseline retires itself at release time, not at test time.
// TestAnonymousPullabilityBaselineIsCurrent only checks that a baselined name
// still corresponds to an image some module references -- it never touches
// the network, so it cannot see whether the underlying package went public.
// verifyImageAnonymouslyPullable does: when a baselined image's release-time
// probe comes back pullable, the release fails and names the stale entry to
// delete, instead of reporting success while a baseline that no longer
// describes reality quietly disables the very check it exists to perform.
//
// Empty right now: ghcr.io/sophium/erun-dns01-webhook was the only entry and
// the package has since been made public, so it was removed rather than left
// as a stale example.
var AnonymousPullabilityBaseline = map[string]bool{}

// verifyModuleReferencedImagesAnonymouslyPullable asserts that every erun
// image a Terraform module references resolves with no credential at all, at
// the version this release just published. A name absent from
// anonymousPullabilityBaseline must resolve anonymously or the release fails;
// a baselined name is reported and let through instead, so a known-private
// image does not block every release until the sophium org's package
// visibility changes -- unless it turns out to already be pullable, in which
// case the baseline entry itself is the defect (see verifyImageAnonymouslyPullable).
func verifyModuleReferencedImagesAnonymouslyPullable(ctx core.Context, projectRoot string, execution shared.BuildExecutionSpec, probe shared.AnonymousPullProbeFunc) error {
	names, err := DiscoverModuleReferencedImageNames(filepath.Join(strings.TrimSpace(projectRoot), "erun-devops", "terraform-erun"))
	if err != nil {
		return fmt.Errorf("scan terraform modules for image references: %w", err)
	}
	if len(names) == 0 {
		return nil
	}

	byName := make(map[string]helm.DockerImageReference, len(execution.DockerPushes))
	for _, pushInput := range execution.DockerPushes {
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
// A baselined name that probes as pullable fails the release rather than
// passing silently -- see anonymousPullabilityBaseline's doc comment for why
// this enforcement has to live here and not in a unit test.
func verifyImageAnonymouslyPullable(ctx core.Context, name string, image helm.DockerImageReference, probe shared.AnonymousPullProbeFunc) error {
	tag := strings.TrimSpace(image.Tag)
	if !registry.IsGHCRRegistry(registry.DockerRegistryFromImageTag(tag)) {
		return nil
	}
	ctx.Trace("release: anonymous-pullability probing " + tag + " with a fresh, credential-free token")
	if ctx.DryRun {
		return nil
	}
	repoPath := registry.DockerNamespaceFromTag(tag) + "/" + name
	pullable, probeErr := probe(context.Background(), nil, repoPath, image.Version)
	if probeErr != nil {
		// A genuine failure to reach the registry (not a refusal -- those come
		// back as pullable=false, no error) tells us nothing new about an
		// image already recorded as known-not-anonymously-pullable, so it
		// must not block a release over network flakiness alone. For any
		// other image, whether the probe could even run is exactly what this
		// check exists to answer, so the release still fails loudly.
		if AnonymousPullabilityBaseline[name] {
			ctx.Info("==> Anonymous-pullability probe for " + tag + " could not run (" + probeErr.Error() + "), but it is already baselined as not anonymously pullable (see anonymousPullabilityBaseline) -- proceeding")
			return nil
		}
		return fmt.Errorf("probe anonymous pull of %s: %w", tag, probeErr)
	}
	if pullable {
		if AnonymousPullabilityBaseline[name] {
			return fmt.Errorf(
				"image %s is baselined in anonymousPullabilityBaseline as not anonymously pullable, but it just probed as pullable -- the entry is now stale and would silently disable this check every release from here on; remove %q from anonymousPullabilityBaseline in release_anonymous_pullability.go",
				tag, name)
		}
		ctx.Info("==> Verified anonymously pullable: " + tag)
		return nil
	}
	if AnonymousPullabilityBaseline[name] {
		ctx.Info("==> Not anonymously pullable (baselined, see anonymousPullabilityBaseline): " + tag)
		return nil
	}
	return fmt.Errorf(
		"image %s resolves for the publishing credential but not anonymously -- a cluster with no pull secret for it cannot pull this image.\n"+
			"Either make the ghcr.io/sophium/%s package public, or add %q to anonymousPullabilityBaseline in release_anonymous_pullability.go with a comment explaining why",
		tag, name, name)
}
