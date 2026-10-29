package dockerbuild

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/buildtarget"
	"github.com/sophium/erun/erun-common/base/seam/imagebuild"
	"github.com/sophium/erun/erun-common/primitive/shared/buildversion"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

func ResolveDockerImageReferenceForProject(ctx core.Context, now buildtarget.NowFunc, projectRoot, environment, buildDir, versionOverride, selectedComponent string) (model.DockerImageReference, error) {
	registry, insecure, err := ResolveDockerBuildRegistryForEnvironment(ctx, projectRoot, environment)
	if err != nil {
		return model.DockerImageReference{}, err
	}

	imageName := strings.TrimSpace(filepath.Base(buildDir))
	if imageName == "" || imageName == "." || imageName == string(filepath.Separator) {
		return model.DockerImageReference{}, fmt.Errorf("could not determine image name from current directory")
	}

	version, baseVersion, versionFromBuildDir, versionFilePath, err := resolveDockerImageVersion(now, projectRoot, buildDir, versionOverride, selectedComponent)
	if err != nil {
		return model.DockerImageReference{}, err
	}

	ref := model.DockerImageReference{
		ProjectRoot:         projectRoot,
		Environment:         strings.TrimSpace(environment),
		Registry:            registry,
		ImageName:           imageName,
		Version:             version,
		Tag:                 fmt.Sprintf("%s/%s:%s", strings.TrimRight(registry, "/"), imageName, version),
		VersionFilePath:     versionFilePath,
		VersionFromBuildDir: versionFromBuildDir,
		Insecure:            insecure,
	}
	if baseVersion != version {
		ref.BaseVersion = baseVersion
	}
	return ref, nil
}

// resolveDockerImageVersion mints a snapshot unless an override or a
// version-from-build-dir file pins the version.
func resolveDockerImageVersion(now buildtarget.NowFunc, projectRoot, buildDir, versionOverride, selectedComponent string) (string, string, bool, string, error) {
	baseVersion, versionFromBuildDir, versionFilePath, err := buildversion.ResolveDockerBuildVersion(buildDir, projectRoot, selectedComponent)
	if err != nil {
		return "", "", false, "", err
	}

	if versionOverride = strings.TrimSpace(versionOverride); versionOverride != "" {
		if versionFromBuildDir {
			return baseVersion, baseVersion, versionFromBuildDir, versionFilePath, nil
		}
		return versionOverride, versionOverride, versionFromBuildDir, versionFilePath, nil
	}

	if versionFromBuildDir {
		return baseVersion, baseVersion, versionFromBuildDir, versionFilePath, nil
	}
	return formatLocalSnapshotVersion(baseVersion, now()), baseVersion, versionFromBuildDir, versionFilePath, nil
}

func NewDockerBuildSpec(ctx core.Context, store buildtarget.DockerStore, now buildtarget.NowFunc, projectRoot, environment string, buildContext model.DockerBuildContext, versionOverride string, platformOverride []string, selectedComponent string) (imagebuild.DockerBuildSpec, error) {
	if strings.TrimSpace(buildContext.DockerfilePath) == "" {
		var err error
		buildContext, err = dockerspec.DockerBuildContextAtDir(buildContext.Dir)
		if err != nil {
			return imagebuild.DockerBuildSpec{}, err
		}
	}

	if err := dockerspec.CheckImageDockerfile(buildContext.DockerfilePath); err != nil {
		return imagebuild.DockerBuildSpec{}, err
	}

	contextDir, err := ResolveDockerBuildContextDirForProject(buildContext.Dir, projectRoot, selectedComponent)
	if err != nil {
		return imagebuild.DockerBuildSpec{}, err
	}
	imageRef, err := ResolveDockerImageReferenceForProject(ctx, now, projectRoot, environment, buildContext.Dir, versionOverride, selectedComponent)
	if err != nil {
		return imagebuild.DockerBuildSpec{}, err
	}

	// "1.0.51" alone would falsely claim a release.
	if imageRef.BaseVersion != "" {
		imageRef.BaseVersion += "-snapshot"
	}

	platforms, err := ResolveDockerBuildPlatforms(ctx, projectRoot, environment, platformOverride)
	if err != nil {
		return imagebuild.DockerBuildSpec{}, err
	}

	build := imagebuild.DockerBuildSpec{
		ContextDir:     contextDir,
		DockerfilePath: buildContext.DockerfilePath,
		Image:          imageRef,
		Platforms:      platforms,
	}
	ApplyDindResourceBuildArgs(store, projectRoot, environment, &build)
	if err := dockerspec.ApplyDockerSecrets(ctx, projectRoot, environment, &build); err != nil {
		return imagebuild.DockerBuildSpec{}, err
	}
	build.CgroupParent = core.BuildContainerCPUCapCgroupParent()
	return build, nil
}
