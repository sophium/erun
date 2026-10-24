package release

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
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerregistry"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

// moduleReferencedImagePattern matches on repository only: a module may interpolate the version.
var moduleReferencedImagePattern = regexp.MustCompile(`ghcr\.io/sophium/(erun-[a-zA-Z0-9_-]+):`)

// DiscoverModuleReferencedImageNames scans Terraform under root for erun images; a
// missing root yields no names.
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

// AnonymousPullabilityBaseline is a shrink-only list of module-referenced images whose
// ghcr package is still private. A release fails when a baselined image probes pullable.
var AnonymousPullabilityBaseline = map[string]bool{}

// verifyModuleReferencedImagesAnonymouslyPullable requires every module-referenced image
// to pull without credentials at this version, unless baselined.
func verifyModuleReferencedImagesAnonymouslyPullable(ctx core.Context, projectRoot string, execution dockerspec.BuildExecutionSpec, probe dockerregistry.AnonymousPullProbeFunc) error {
	names, err := DiscoverModuleReferencedImageNames(filepath.Join(strings.TrimSpace(projectRoot), "erun-devops", "terraform-erun"))
	if err != nil {
		return fmt.Errorf("scan terraform modules for image references: %w", err)
	}
	if len(names) == 0 {
		return nil
	}

	byName := make(map[string]model.DockerImageReference, len(execution.DockerPushes))
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

func verifyImageAnonymouslyPullable(ctx core.Context, name string, image model.DockerImageReference, probe dockerregistry.AnonymousPullProbeFunc) error {
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
		// An unreachable registry says nothing new about a baselined image, so only
		// unbaselined images fail on it.
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
