package eruncommon

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// deployRuntimeImagePullProbe is the anonymousPullProbeFunc RunHelmDeploy
// calls; a var (like ensureDeployNamespace above it in deploy.go) so a test
// can swap in a deterministic result instead of reaching live ghcr.io.
var deployRuntimeImagePullProbe anonymousPullProbeFunc = probeAnonymousManifestPull

// autoImagePullSecretName is the dockerconfigjson Secret erun provisions and
// attaches to the runtime pod on its own, without requiring an operator to
// have named one via `--image-pull-secret`. It is distinct from any
// operator-named entry in EnvConfig.ImagePullSecrets so this mechanism never
// collides with, or silently repurposes, a secret an operator configured by
// hand.
func autoImagePullSecretName(tenant string) string {
	return RuntimeReleaseName(tenant) + "-image-pull"
}

// ensureRuntimeImagePullSecret is the deploy-time preflight against a private
// runtime image with no pull credential: an environment that had never needed
// a pull secret (it rode the anonymously
// pullable stock erun-devops image) kept deploying with none configured even
// after it moved onto a tenant's own private image line, because
// EnvConfig.ImagePullSecrets is only ever populated by an operator's explicit
// `--image-pull-secret` at init time -- nothing re-derives it when the
// runtime image itself changes. Every runtime chart template renders
// `strategy: Recreate` (required by the RWO home/docker-state volumes every
// chart mounts), so the previous pod is torn down before the new one is even
// scheduled: an unpullable image does not degrade the rollout, it takes the
// environment down. This runs before any cluster mutation so a refusal here
// costs nothing.
//
// It only ever inspects the runtime image
// (deployInput.ImageOverrides[DevopsComponentName]): that is the one image
// reference erun resolves on the caller side (resolveDeployRuntimeImage)
// rather than deferring to a chart's own baked-in default. Every other
// component chart names its own image inside its templates, which this
// package has no way to reproduce without guessing, so this preflight does
// not attempt to cover them -- they still deploy exactly as they did before,
// through whatever ImagePullSecrets an operator named.
//
// Three outcomes:
//   - The image is not on ghcr.io, or the runtime deploy carries no image
//     override at all (the erun product's own environments, which ride the
//     chart's baked-in stock image): no-op, byte-for-byte unchanged.
//   - A ghcr.io credential resolves host-side (the same resolveOCIRegistryBasicAuth
//     every other pull-secret path already uses): deployInput.ImagePullSecrets
//     gains an auto-provisioned entry so the existing refreshImagePullSecrets /
//     applyImagePullSecrets / helmImagePullSecretSetArgs plumbing mints and
//     attaches it, on the same terms as an operator-named secret. A resolved
//     credential is attached unconditionally, whether or not the image turns
//     out to be private -- a valid host credential does not stop a public
//     image from pulling, so this never risks the working case.
//   - No credential resolves: the only way to know whether the deploy is
//     actually at risk is to ask the registry. A dry run cannot make a live
//     network call (the same tradeoff release's own anonymous-pullability
//     check makes for --dry-run), so it states the situation and defers the
//     real determination to a real run. A real run probes; a pullable image
//     proceeds unchanged (the legitimately-public, no-credential-configured
//     case), and anything else -- confirmed private, or the probe itself
//     could not get an answer -- refuses. An inconclusive probe is never
//     treated as "public": that is exactly the assumption that let this
//     failure hide until the image actually went private.
func ensureRuntimeImagePullSecret(ctx Context, deployInput *HelmDeploySpec, probe anonymousPullProbeFunc) error {
	image := strings.TrimSpace(deployInput.ImageOverrides[DevopsComponentName])
	if image == "" {
		return nil
	}
	host := dockerRegistryFromImageTag(image)
	if !isGHCRRegistry(host) {
		return nil
	}
	_, repoName, tag, ok := parseDockerImageReference(image)
	if !ok {
		return nil
	}

	if _, ok := resolveOCIRegistryBasicAuth(host); ok {
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

	repoPath := DockerNamespaceFromTag(image) + "/" + repoName
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
