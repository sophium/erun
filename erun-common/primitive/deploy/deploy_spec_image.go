package deploy

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// resolveInstallExistingVersionDeploySpec resolves a deploy that installs the
// image already published at the pinned version, without building or pushing.
// A builds-here env addresses the published tag by reference rather than
// rebuilding the working tree under that label. The chart's referenced images
// are verified to exist so a version that was never built fails fast. helm still
// runs (no builds means SkipHelm stays false).
func resolveInstallExistingVersionDeploySpec(ctx core.Context, store DeployStore, target configpkg.OpenResult, deployContext shared.KubernetesDeployContext, version string, versionFromPersist bool) (DeploySpec, error) {
	deployInput, err := newHelmDeploySpec(target, deployContext, version)
	if err != nil {
		return DeploySpec{}, err
	}
	// Pull-path provenance: when installing the persisted version (a --current
	// redeploy or an open ensure), address the same registry the previous
	// deploy used, so a reopen survives the operator editing the project's
	// container registry.
	if versionFromPersist && deployContextOwnsRuntimeChart(deployContext, target.Tenant) {
		if registry := strings.TrimSpace(target.EnvConfig.RuntimeRegistry); registry != "" {
			deployInput.ContainerRegistry = registry
		}
	}
	// registryCredentialSecretName is a runtime-chart-only value (it mounts into
	// the erun-devops container's docker config); a component deploy carries
	// nothing, since newHelmDeploySpecWithValues never sets it.
	if deployContextOwnsRuntimeChart(deployContext, target.Tenant) {
		deployInput.RegistryCredentialSecretName = strings.TrimSpace(target.EnvConfig.RegistryCredentialSecretName)
		deployInput.PlatformAliasSecretName = strings.TrimSpace(target.EnvConfig.PlatformAliasSecretName)
	}
	// The version alone governs the reset decision, same as a fresh build's
	// deploy below: a released, non-snapshot version must never wipe.
	deployInput.ResetDatabase = deployResetsDatabase(deployInput.Version)
	if err := configureDeployInputMetadata(store, target, &deployInput); err != nil {
		return DeploySpec{}, err
	}

	ctx.Trace("deploy: version " + deployInput.Version + " pinned; installing the published image by reference (deploy never builds)")
	images, err := findDockerImagesInChart(deployContext.ChartPath, deployInput.Version)
	if err != nil {
		return DeploySpec{}, err
	}
	for _, image := range images {
		if err := verifyExistingDeployImage(ctx, image, deployInput.Version, deployInput.ContainerRegistry, target.ClusterRegistryInsecure); err != nil {
			return DeploySpec{}, err
		}
	}

	return DeploySpec{
		Target:        target,
		DeployContext: deployContext,
		Deploy:        deployInput,
	}, nil
}

// verifyExistingDeployImage fails when the image a chart pulls at the pinned
// version is absent both locally and in the registry. deploy installs an
// existing version; a missing image means the version was never built, which
// is an error, not a trigger to build it from the working tree.
//
// Only images at the deploy version are checked: charts also reference pinned
// infra/base images (dind, binfmt) at their own versions, which are not the
// version being installed and are skipped. Registry-less chart refs (the app
// images, which get their registry from --set containerRegistry) are qualified
// with the deploy registry so the lookup hits the published tag. Dry-run traces
// the lookup and skips the network so the plan stays offline and deterministic;
// a registry error that is not a definitive "absent" does not block the deploy
// (the rollout would surface a real pull failure).
func verifyExistingDeployImage(ctx core.Context, ref, version, registry string, insecure bool) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	nameTag := ref
	qualified := false
	if idx := strings.LastIndex(ref, "/"); idx >= 0 {
		nameTag = ref[idx+1:]
		qualified = true
	}
	_, refVersion, ok := strings.Cut(nameTag, ":")
	if !ok || strings.TrimSpace(refVersion) != strings.TrimSpace(version) {
		// Not the version being installed (pinned infra/base image) — not ours
		// to verify.
		return nil
	}

	tag := ref
	if !qualified && strings.TrimSpace(registry) != "" {
		tag = strings.TrimRight(strings.TrimSpace(registry), "/") + "/" + ref
	}

	ctx.TraceCommand("", "docker", append(shared.DockerManifestArgs("inspect", insecure), tag)...)
	if ctx.DryRun {
		return nil
	}
	return confirmDeployImagePresent(ctx, tag, version, insecure)
}

// confirmDeployImagePresent checks that the pinned tag exists locally or in the
// registry, returning an error only when its absence is definitive. A registry
// error that is not a "absent" verdict (network/auth) is traced and tolerated:
// the rollout surfaces a real pull failure if the image is genuinely missing.
func confirmDeployImagePresent(ctx core.Context, tag, version string, insecure bool) error {
	remote, remoteErr := shared.DockerManifestExists(tag, insecure)
	if remote {
		return nil
	}
	if local, _ := shared.DockerImageExists(tag); local {
		return nil
	}
	if remoteErr != nil {
		ctx.Trace(fmt.Sprintf("deploy: could not verify %s in the registry (%v); proceeding", tag, remoteErr))
		return nil
	}
	return fmt.Errorf("deploy --version %s: image %s is not present locally or in the registry; deploy installs an existing version and does not build it — run erun build/push to create it first", version, tag)
}

func configureDeployInputMetadata(store DeployStore, target configpkg.OpenResult, deployInput *HelmDeploySpec) error {
	issuers, err := cloud.ResolveTenantCloudProviderIssuers(store, target.TenantConfig)
	if err != nil {
		return err
	}
	deployInput.OIDCAllowedIssuers = joinOIDCAllowedIssuers(issuers, deployInput.Platform)
	managedCloud, err := activity.ManagedCloudEnvironment(store, target.EnvConfig)
	if err != nil {
		return err
	}
	deployInput.ManagedCloud = managedCloud
	deployInput.UseHostCredentials = target.EnvConfig.HasAWSCloudAlias()
	gateway, err := configpkg.ResolveOpenRouterConfig(store)
	if err != nil {
		return err
	}
	deployInput.OpenRouter = gateway
	applyCloudProviderDeployMetadata(store, target.EnvConfig, deployInput)
	applyCloudflareDeployMetadata(store, target.EnvConfig, deployInput)
	if managedCloud {
		applyCloudContextStopMetadata(store, target.EnvConfig, deployInput)
	}
	return nil
}

// joinOIDCAllowedIssuers renders the API's trusted-issuer list: the tenant's
// cloud-provider issuers plus, for a platform deployment, its own hosted IdP.
// The platform's IdP is the issuer its own control plane authenticates operators
// against, and the auth host is already declared in the platform block, so a
// deploy that left it out resolved a list the console's issuer was absent from —
// every hosted sign-in rejected until someone patched the Deployment by hand.
func joinOIDCAllowedIssuers(cloudIssuers []string, platform configpkg.PlatformConfig) string {
	issuers := slices.Clone(cloudIssuers)
	if authHost := strings.TrimSpace(platform.Resolve().AuthHost); authHost != "" {
		issuer := "https://" + authHost
		if !slices.Contains(issuers, issuer) {
			issuers = append(issuers, issuer)
		}
	}
	return strings.Join(issuers, ",")
}

// helmOIDCAllowedIssuersSetArgs renders the trusted-issuer --set, guarded on a
// non-empty computed value so an env with no resolved issuer leaves
// api.oidcAllowedIssuers unset. helm's --set always wins over -f, so an
// unconditional empty --set-string silently clobbered whatever the operator
// configured under that key in values.<env>.yaml; omitting the flag lets -f
// apply, and this same guard still emits the flag whenever the computation
// does resolve at least one issuer.
func helmOIDCAllowedIssuersSetArgs(issuers string) []string {
	if strings.TrimSpace(issuers) == "" {
		return nil
	}
	return []string{"--set-string", "api.oidcAllowedIssuers=" + escapeHelmSetValue(issuers)}
}

// resolveDeploySpecForCurrentDockerBuild builds the pure deploy plan for a
// `build --deploy` orchestration: the working-tree image has already been
// built and pushed by the build phase, so deploy references it by tag via an
// ImageOverride and never builds. SkipHelm stays false (a build-and-deploy
// rolls the chart); the orchestrator may set it when nothing was rebuilt.
func resolveDeploySpecForCurrentDockerBuild(store DeployStore, target configpkg.OpenResult, deployContext shared.KubernetesDeployContext, build shared.DockerBuildSpec) (DeploySpec, error) {
	deployInput, err := newHelmDeploySpec(target, deployContext, "")
	if err != nil {
		return DeploySpec{}, err
	}
	deployInput.ResetDatabase = deployResetsDatabase(build.Image.Version)
	if err := configureDeployInputMetadata(store, target, &deployInput); err != nil {
		return DeploySpec{}, err
	}
	deployInput.ImageOverrides = map[string]string{
		build.Image.ImageName: build.Image.Tag,
	}
	if build.Image.ImageName == configpkg.DevopsComponentName {
		deployInput.ResolvedRuntimeImage = build.Image.Tag
		// A build --deploy of the devops component is itself the operator's
		// explicit choice of which image this env runs -- record it the same
		// self-maintaining, bare-name way an operator's own --runtime-image
		// override does, so a later plain `erun deploy`/`erun open` reads back
		// the line this build just moved the env onto instead of the stale
		// value erun#1754 found left behind by this exact orchestration.
		if name := configpkg.RuntimeImageComponentName(build.Image.Tag); name != "" {
			deployInput.PersistRuntimeImage = name
		}
	}

	return DeploySpec{
		Target:        target,
		DeployContext: deployContext,
		Deploy:        deployInput,
	}, nil
}

// resolveCurrentDockerComponentBuildForDeploy resolves the working-tree docker
// build of the current component for a `build --deploy` orchestration. The
// caller gates this on the env being a builds-here type; this helper only
// requires that the current directory is a docker build context
// (<module>/docker/<component>). Returns nil when there is no such context.
func resolveCurrentDockerComponentBuildForDeploy(ctx core.Context, store shared.DockerStore, findProjectRoot shared.ProjectFinderFunc, resolveDockerBuildContext shared.BuildContextResolverFunc, now shared.NowFunc, projectRoot, environment, versionOverride string) (*shared.DockerBuildSpec, error) {
	store, _, resolveDockerBuildContext, now = shared.NormalizeDockerDependencies(store, findProjectRoot, resolveDockerBuildContext, now)
	buildContext, ok, err := currentDockerComponentBuildContext(resolveDockerBuildContext)
	if err != nil || !ok {
		return nil, err
	}

	build, err := shared.NewDockerBuildSpec(ctx, store, now, projectRoot, environment, buildContext, versionOverride, nil, "")
	if err != nil {
		return nil, err
	}
	return &build, nil
}

// currentDockerComponentBuildContext resolves the current directory's docker
// build context; ok is false when it is not a <module>/docker/<component> one.
func currentDockerComponentBuildContext(resolveDockerBuildContext shared.BuildContextResolverFunc) (shared.DockerBuildContext, bool, error) {
	if resolveDockerBuildContext == nil {
		return shared.DockerBuildContext{}, false, nil
	}
	buildContext, err := resolveDockerBuildContext()
	if err != nil {
		return shared.DockerBuildContext{}, false, err
	}
	ok := strings.TrimSpace(buildContext.DockerfilePath) != "" && filepath.Base(filepath.Dir(buildContext.Dir)) == "docker"
	return buildContext, ok, nil
}

func deployContextOwnsRuntimeChart(deployContext shared.KubernetesDeployContext, tenant string) bool {
	name := strings.TrimSpace(deployContext.ComponentName)
	if name == "" {
		return false
	}
	// Match either runtime alias (<tenant>-devops or the canonical erun-devops)
	// so a tenant tree that vendors the runtime chart under either name resolves
	// — the same dual-lookup erun open uses (runtimeComponentNames).
	return slices.Contains(runtimeComponentNames(tenant), name)
}

func deployContextOwnsDockerBuild(deployContext shared.KubernetesDeployContext, build shared.DockerBuildSpec) bool {
	chartPath := filepath.Clean(strings.TrimSpace(deployContext.ChartPath))
	dockerfilePath := filepath.Clean(strings.TrimSpace(build.DockerfilePath))
	if chartPath == "" || dockerfilePath == "" {
		return false
	}

	moduleRoot := filepath.Dir(filepath.Dir(chartPath))
	buildDir := filepath.Dir(dockerfilePath)
	relative, err := filepath.Rel(moduleRoot, buildDir)
	if err != nil || relative == "." {
		return false
	}
	return !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && relative != ".."
}

func applyDeployKubernetesContext(store DeployStore, target configpkg.OpenResult) configpkg.OpenResult {
	if resolver, ok := store.(deployKubernetesContextResolver); ok {
		target.EnvConfig.KubernetesContext = resolver.ResolveDeployKubernetesContext(target.Environment, target.EnvConfig.KubernetesContext)
	}
	return target
}

func ResolveOpenRuntimeDeploySpec(ctx core.Context, store DeployStore, findProjectRoot shared.ProjectFinderFunc, resolveDockerBuildContext shared.BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now shared.NowFunc, target configpkg.OpenResult, allowLocalBuilds bool) (DeploySpec, error) {
	if err := refuseHostEnvironmentOperation("deploy", target); err != nil {
		return DeploySpec{}, err
	}
	store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now = normalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)
	now = freezeNow(now)
	spec, err := resolveOpenRuntimeDeploySpec(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target, allowLocalBuilds)
	if err != nil {
		return DeploySpec{}, err
	}
	// open's heal-redeploy must rethread the env's recorded MCP-auth key too, or
	// reopening an authenticated env would replace its pod with an
	// unauthenticated edge.
	if err := ApplyMCPAuthToRuntimeSpec(ctx, DeployTarget{Tenant: target.Tenant, Environment: target.Environment}, &spec); err != nil {
		return DeploySpec{}, err
	}
	return spec, nil
}
