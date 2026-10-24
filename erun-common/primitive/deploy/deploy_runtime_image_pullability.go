package deploy

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// A var so tests avoid reaching live ghcr.io.
var deployRuntimeImagePullProbe shared.AnonymousPullProbeFunc = shared.ProbeAnonymousManifestPull

// autoImagePullSecretName is distinct from operator-named secrets so it never
// repurposes one.
func autoImagePullSecretName(tenant string) string {
	return config.RuntimeReleaseName(tenant) + "-image-pull"
}

// ensureRuntimeImagePullSecret guards a ghcr.io runtime image before any cluster
// mutation: Recreate tears the old pod down first, so an unpullable image takes
// the env down. A resolved credential is always attached; otherwise a real run
// probes anonymous pullability and refuses unless it is confirmed pullable.
func ensureRuntimeImagePullSecret(ctx core.Context, deployInput *HelmDeploySpec, probe shared.AnonymousPullProbeFunc) error {
	image := strings.TrimSpace(deployInput.ImageOverrides[config.DevopsComponentName])
	if image == "" {
		return nil
	}
	host := registry.DockerRegistryFromImageTag(image)
	if !registry.IsGHCRRegistry(host) {
		return nil
	}
	_, repoName, tag, ok := shared.ParseDockerImageReference(image)
	if !ok {
		return nil
	}

	if _, ok := registry.ResolveOCIRegistryBasicAuth(host); ok {
		name := autoImagePullSecretName(deployInput.Tenant)
		if !slices.Contains(deployInput.ImagePullSecrets, name) {
			deployInput.ImagePullSecrets = append(append([]string(nil), deployInput.ImagePullSecrets...), name)
		}
		ctx.Trace("image pull: " + image + " has a resolvable " + host + " credential; attaching auto-provisioned secret " + name)
		return nil
	}

	ctx.Trace("image pull: " + image + " has no resolvable " + host + " credential")
	if ctx.DryRun {
		ctx.Trace("image pull: anonymous pullability of " + image + " is not probed in dry-run; a real deploy refuses before rollout if it turns out not to be anonymously pullable")
		return nil
	}

	repoPath := registry.DockerNamespaceFromTag(image) + "/" + repoName
	pullable, err := probe(context.Background(), nil, repoPath, tag)
	if err != nil {
		return fmt.Errorf("could not determine whether %s is pullable with no credential, and none resolved to provision a pull secret: %w", image, err)
	}
	if !pullable {
		return fmt.Errorf("%s is not anonymously pullable and no %s credential resolved to provision a pull secret -- refusing before the rollout recreates the running pod", image, host)
	}
	ctx.Trace("image pull: " + image + " is anonymously pullable; no pull secret required")
	return nil
}
