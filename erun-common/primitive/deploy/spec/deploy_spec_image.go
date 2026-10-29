package spec

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/openrouter"
	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/buildtarget"
	"github.com/sophium/erun/erun-common/base/seam/deploytarget"
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
	"github.com/sophium/erun/erun-common/base/seam/imagebuild"
	"github.com/sophium/erun/erun-common/base/seam/projectroot"
	"github.com/sophium/erun/erun-common/infra/activity/managedcloud"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
	"github.com/sophium/erun/erun-common/primitive/deploy/chartfiles"
	"github.com/sophium/erun/erun-common/primitive/deploy/chartref"
	"github.com/sophium/erun/erun-common/primitive/deploy/helmspec"
	"github.com/sophium/erun/erun-common/primitive/deploy/mcpauth"
	targetpkg "github.com/sophium/erun/erun-common/primitive/deploy/target"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerbuild"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerregistry"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

// resolveInstallExistingVersionDeploySpec installs the image already published at
// the pinned version, verifying it exists; it never builds.
func resolveInstallExistingVersionDeploySpec(ctx core.Context, store deploytarget.DeployStore, target configpkg.OpenResult, deployContext model.KubernetesDeployContext, version string, versionFromPersist bool) (helmdeploy.DeploySpec, error) {
	deployInput, err := helmspec.NewHelmDeploySpec(target, deployContext, version)
	if err != nil {
		return helmdeploy.DeploySpec{}, err
	}
	// Reuse the previous deploy's registry so a reopen survives a registry edit.
	if versionFromPersist && chartref.DeployContextOwnsRuntimeChart(deployContext, target.Tenant) {
		if registry := strings.TrimSpace(target.EnvConfig.RuntimeRegistry); registry != "" {
			deployInput.ContainerRegistry = registry
		}
	}
	// Runtime-chart-only values.
	if chartref.DeployContextOwnsRuntimeChart(deployContext, target.Tenant) {
		deployInput.RegistryCredentialSecretName = strings.TrimSpace(target.EnvConfig.RegistryCredentialSecretName)
		deployInput.PlatformAliasSecretName = strings.TrimSpace(target.EnvConfig.PlatformAliasSecretName)
	}
	// A released, non-snapshot version must never wipe the database.
	deployInput.ResetDatabase = deployResetsDatabase(deployInput.Version)
	if err := configureDeployInputMetadata(store, target, &deployInput); err != nil {
		return helmdeploy.DeploySpec{}, err
	}

	ctx.Trace("deploy: version " + deployInput.Version + " pinned; installing the published image by reference (deploy never builds)")
	images, err := chartfiles.FindDockerImagesInChart(deployContext.ChartPath, deployInput.Version)
	if err != nil {
		return helmdeploy.DeploySpec{}, err
	}
	for _, image := range images {
		if err := verifyExistingDeployImage(ctx, image, deployInput.Version, deployInput.ContainerRegistry, target.ClusterRegistryInsecure); err != nil {
			return helmdeploy.DeploySpec{}, err
		}
	}

	return helmdeploy.DeploySpec{
		Target:        target,
		DeployContext: deployContext,
		Deploy:        deployInput,
	}, nil
}

// verifyExistingDeployImage fails when a chart image at the pinned version is
// absent: deploy never builds it. Images at other versions (infra/base) are
// skipped, and registry-less refs are qualified with the deploy registry.
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
		return nil
	}

	tag := ref
	if !qualified && strings.TrimSpace(registry) != "" {
		tag = strings.TrimRight(strings.TrimSpace(registry), "/") + "/" + ref
	}

	ctx.TraceCommand("", "docker", append(dockerspec.DockerManifestArgs("inspect", insecure), tag)...)
	if ctx.DryRun {
		return nil
	}
	return confirmDeployImagePresent(ctx, tag, version, insecure)
}

// A non-definitive registry error is tolerated; the rollout surfaces a real pull
// failure.
func confirmDeployImagePresent(ctx core.Context, tag, version string, insecure bool) error {
	remote, remoteErr := dockerregistry.DockerManifestExists(tag, insecure)
	if remote {
		return nil
	}
	if local, _ := dockerregistry.DockerImageExists(tag); local {
		return nil
	}
	if remoteErr != nil {
		ctx.Trace(fmt.Sprintf("deploy: could not verify %s in the registry (%v); proceeding", tag, remoteErr))
		return nil
	}
	return fmt.Errorf("deploy --version %s: image %s is not present locally or in the registry; deploy installs an existing version and does not build it — run erun build/push to create it first", version, tag)
}

func configureDeployInputMetadata(store deploytarget.DeployStore, target configpkg.OpenResult, deployInput *helmdeploy.HelmDeploySpec) error {
	issuers, err := providerconfig.ResolveTenantCloudProviderIssuers(store, target.TenantConfig)
	if err != nil {
		return err
	}
	deployInput.OIDCAllowedIssuers = joinOIDCAllowedIssuers(issuers, deployInput.Platform)
	managedCloud, err := managedcloud.ManagedCloudEnvironment(store, target.EnvConfig)
	if err != nil {
		return err
	}
	deployInput.ManagedCloud = managedCloud
	deployInput.UseHostCredentials = target.EnvConfig.HasAWSCloudAlias()
	gateway, err := openrouter.ResolveOpenRouterConfig(store)
	if err != nil {
		return err
	}
	deployInput.OpenRouter = gateway
	helmspec.ApplyCloudProviderDeployMetadata(store, target.EnvConfig, deployInput)
	applyCloudflareDeployMetadata(store, target.EnvConfig, deployInput)
	if managedCloud {
		helmspec.ApplyCloudContextStopMetadata(store, target.EnvConfig, deployInput)
	}
	return nil
}

// joinOIDCAllowedIssuers adds the platform's own IdP, without which every hosted
// console sign-in is rejected.
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

// resolveDeploySpecForCurrentDockerBuild references the image the build phase
// already pushed; it never builds.
func resolveDeploySpecForCurrentDockerBuild(store deploytarget.DeployStore, target configpkg.OpenResult, deployContext model.KubernetesDeployContext, build imagebuild.DockerBuildSpec) (helmdeploy.DeploySpec, error) {
	deployInput, err := helmspec.NewHelmDeploySpec(target, deployContext, "")
	if err != nil {
		return helmdeploy.DeploySpec{}, err
	}
	deployInput.ResetDatabase = deployResetsDatabase(build.Image.Version)
	if err := configureDeployInputMetadata(store, target, &deployInput); err != nil {
		return helmdeploy.DeploySpec{}, err
	}
	deployInput.ImageOverrides = map[string]string{
		build.Image.ImageName: build.Image.Tag,
	}
	if build.Image.ImageName == runtimetarget.DevopsComponentName {
		deployInput.ResolvedRuntimeImage = build.Image.Tag
		// Persist the bare name so later deploys read back the line this build chose.
		if name := configpkg.RuntimeImageComponentName(build.Image.Tag); name != "" {
			deployInput.PersistRuntimeImage = name
		}
	}

	return helmdeploy.DeploySpec{
		Target:        target,
		DeployContext: deployContext,
		Deploy:        deployInput,
	}, nil
}

func resolveCurrentDockerComponentBuildForDeploy(ctx core.Context, store buildtarget.DockerStore, findProjectRoot projectroot.ProjectFinderFunc, resolveDockerBuildContext buildtarget.BuildContextResolverFunc, now buildtarget.NowFunc, projectRoot, environment, versionOverride string) (*imagebuild.DockerBuildSpec, error) {
	store, _, resolveDockerBuildContext, now = dockerbuild.NormalizeDockerDependencies(store, findProjectRoot, resolveDockerBuildContext, now)
	buildContext, ok, err := currentDockerComponentBuildContext(resolveDockerBuildContext)
	if err != nil || !ok {
		return nil, err
	}

	build, err := dockerbuild.NewDockerBuildSpec(ctx, store, now, projectRoot, environment, buildContext, versionOverride, nil, "")
	if err != nil {
		return nil, err
	}
	return &build, nil
}

func currentDockerComponentBuildContext(resolveDockerBuildContext buildtarget.BuildContextResolverFunc) (model.DockerBuildContext, bool, error) {
	if resolveDockerBuildContext == nil {
		return model.DockerBuildContext{}, false, nil
	}
	buildContext, err := resolveDockerBuildContext()
	if err != nil {
		return model.DockerBuildContext{}, false, err
	}
	ok := strings.TrimSpace(buildContext.DockerfilePath) != "" && filepath.Base(filepath.Dir(buildContext.Dir)) == "docker"
	return buildContext, ok, nil
}

func deployContextOwnsDockerBuild(deployContext model.KubernetesDeployContext, build imagebuild.DockerBuildSpec) bool {
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

func applyDeployKubernetesContext(store deploytarget.DeployStore, target configpkg.OpenResult) configpkg.OpenResult {
	if resolver, ok := store.(deployKubernetesContextResolver); ok {
		target.EnvConfig.KubernetesContext = resolver.ResolveDeployKubernetesContext(target.Environment, target.EnvConfig.KubernetesContext)
	}
	return target
}

func ResolveOpenRuntimeDeploySpec(ctx core.Context, store deploytarget.DeployStore, findProjectRoot projectroot.ProjectFinderFunc, resolveDockerBuildContext buildtarget.BuildContextResolverFunc, resolveKubernetesDeployContext deploytarget.DeployContextResolverFunc, now buildtarget.NowFunc, target configpkg.OpenResult, allowLocalBuilds bool) (helmdeploy.DeploySpec, error) {
	if err := targetpkg.RefuseHostEnvironmentOperation("deploy", target); err != nil {
		return helmdeploy.DeploySpec{}, err
	}
	store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now = targetpkg.NormalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)
	now = targetpkg.FreezeNow(now)
	spec, err := resolveOpenRuntimeDeploySpec(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target, allowLocalBuilds)
	if err != nil {
		return helmdeploy.DeploySpec{}, err
	}
	// Rethread the recorded MCP-auth key, or a reopen would replace the pod with an
	// unauthenticated edge.
	if err := mcpauth.ApplyMCPAuthToRuntimeSpec(ctx, model.DeployTarget{Tenant: target.Tenant, Environment: target.Environment}, &spec); err != nil {
		return helmdeploy.DeploySpec{}, err
	}
	return spec, nil
}
