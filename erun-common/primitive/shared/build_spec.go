package shared

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/helm"
)

// ParseDockerImageReference rejects a reference whose "registry" is really a
// version string: helm printf templates embed .Chart.AppVersion as a namespace
// prefix (e.g. "1.0.51-snapshot-.../image:tag"), which is not a valid registry
// host and would fail with a push-time DNS lookup.
func ParseDockerImageReference(image string) (registry, imageName, version string, ok bool) {
	if image == "" {
		return "", "", "", false
	}

	nameTag := image
	if idx := strings.LastIndex(image, "/"); idx >= 0 {
		registry = image[:idx]
		nameTag = image[idx+1:]
	}

	imageName, version, found := strings.Cut(nameTag, ":")
	if !found || strings.TrimSpace(imageName) == "" || strings.TrimSpace(version) == "" {
		return "", "", "", false
	}

	if dockerRegistryLooksLikeVersion(registry) {
		return "", "", "", false
	}

	return registry, imageName, version, true
}

func ResolveDockerImageReferenceForProject(ctx core.Context, now NowFunc, projectRoot, environment, buildDir, versionOverride, selectedComponent string) (helm.DockerImageReference, error) {
	registry, insecure, err := resolveDockerBuildRegistryForEnvironment(ctx, projectRoot, environment)
	if err != nil {
		return helm.DockerImageReference{}, err
	}

	imageName := strings.TrimSpace(filepath.Base(buildDir))
	if imageName == "" || imageName == "." || imageName == string(filepath.Separator) {
		return helm.DockerImageReference{}, fmt.Errorf("could not determine image name from current directory")
	}

	version, baseVersion, versionFromBuildDir, versionFilePath, err := resolveDockerImageVersion(now, projectRoot, buildDir, versionOverride, selectedComponent)
	if err != nil {
		return helm.DockerImageReference{}, err
	}

	ref := helm.DockerImageReference{
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

// resolveDockerImageVersion mints the build version independently of the
// environment: a timestamped snapshot by default, the bare base semver only when
// an explicit override or a version-from-build-dir file pins it (release passes
// the resolved release version as that override).
func resolveDockerImageVersion(now NowFunc, projectRoot, buildDir, versionOverride, selectedComponent string) (string, string, bool, string, error) {
	baseVersion, versionFromBuildDir, versionFilePath, err := ResolveDockerBuildVersion(buildDir, projectRoot, selectedComponent)
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

func NewDockerBuildSpec(ctx core.Context, store DockerStore, now NowFunc, projectRoot, environment string, buildContext DockerBuildContext, versionOverride string, platformOverride []string, selectedComponent string) (DockerBuildSpec, error) {
	if strings.TrimSpace(buildContext.DockerfilePath) == "" {
		var err error
		buildContext, err = DockerBuildContextAtDir(buildContext.Dir)
		if err != nil {
			return DockerBuildSpec{}, err
		}
	}

	if err := checkImageDockerfile(buildContext.DockerfilePath); err != nil {
		return DockerBuildSpec{}, err
	}

	contextDir, err := ResolveDockerBuildContextDirForProject(buildContext.Dir, projectRoot, selectedComponent)
	if err != nil {
		return DockerBuildSpec{}, err
	}
	imageRef, err := ResolveDockerImageReferenceForProject(ctx, now, projectRoot, environment, buildContext.Dir, versionOverride, selectedComponent)
	if err != nil {
		return DockerBuildSpec{}, err
	}

	// Mark local snapshot builds so ERUN_VERSION reads "1.0.51-snapshot", not
	// "1.0.51", which would falsely claim a release.
	if imageRef.BaseVersion != "" {
		imageRef.BaseVersion += "-snapshot"
	}

	platforms, err := ResolveDockerBuildPlatforms(ctx, projectRoot, environment, platformOverride)
	if err != nil {
		return DockerBuildSpec{}, err
	}

	build := DockerBuildSpec{
		ContextDir:     contextDir,
		DockerfilePath: buildContext.DockerfilePath,
		Image:          imageRef,
		Platforms:      platforms,
	}
	ApplyDindResourceBuildArgs(store, projectRoot, environment, &build)
	if err := ApplyDockerSecrets(ctx, projectRoot, environment, &build); err != nil {
		return DockerBuildSpec{}, err
	}
	build.CgroupParent = core.BuildContainerCPUCapCgroupParent()
	return build, nil
}

// checkImageDockerfile refuses a Dockerfile that is not an image component's:
// an e2e suite, a linux package, or one whose test stage nothing consumes.
func checkImageDockerfile(dockerfilePath string) error {
	if err := CheckNotE2ESuiteDockerfile(dockerfilePath); err != nil {
		return err
	}
	if err := checkNotLinuxPackageDockerfile(dockerfilePath); err != nil {
		return err
	}
	return CheckDockerfileTestStageConsumed(dockerfilePath)
}

func (b DockerBuildSpec) TraceCommands() []helm.CommandSpec {
	if b.Promote {
		return promoteTraceCommands(b)
	}
	return multiPlatformTraceCommands(b)
}

func multiPlatformTraceCommands(b DockerBuildSpec) []helm.CommandSpec {
	commands := make([]helm.CommandSpec, 0, len(b.Platforms)*4+2)
	perPlatformTags := make([]string, 0, len(b.Platforms))
	baseTag := StableBaseVersionTag(b.Image)
	for _, platform := range b.Platforms {
		platformTag := PlatformSuffixedTag(b.Image.Tag, platform)
		perPlatformTags = append(perPlatformTags, platformTag)
		commands = append(commands, helm.CommandSpec{
			Dir:  b.ContextDir,
			Name: "docker",
			Args: DockerBuildArgs(b, platform),
		})
		if b.Fingerprint != "" {
			commands = append(commands, dockerTagTraceCommand(b.ContextDir, platformTag, FingerprintTag(b.Image, b.Fingerprint, platform)))
		}
		commands = append(commands, stableBaseVersionTraceCommands(b.ContextDir, platformTag, baseTag, platform)...)
		if b.Push {
			commands = append(commands, helm.CommandSpec{
				Dir:  b.ContextDir,
				Name: "docker",
				Args: dockerPushArgs(platformTag, b.Verbosity),
			})
		}
	}
	if !b.Push {
		return commands
	}
	commands = append(commands, multiPlatformManifestTraceCommands(b.ContextDir, b.Image.Tag, perPlatformTags, b.Image.Insecure)...)
	return commands
}

// stableBaseVersionTraceCommands must mirror tagStableBaseVersionAfterBuild's
// ordering so the dry-run trace stays an honest preview of the real build.
func stableBaseVersionTraceCommands(dir, platformTag, baseTag, platform string) []helm.CommandSpec {
	if baseTag == "" {
		return nil
	}
	commands := make([]helm.CommandSpec, 0, 2)
	if archTag := PlatformSuffixedTag(baseTag, platform); archTag != platformTag {
		commands = append(commands, dockerTagTraceCommand(dir, platformTag, archTag))
	}
	if platformTag != baseTag {
		commands = append(commands, dockerTagTraceCommand(dir, platformTag, baseTag))
	}
	return commands
}

func promoteTraceCommands(b DockerBuildSpec) []helm.CommandSpec {
	commands := make([]helm.CommandSpec, 0, len(b.Platforms)*3+2)
	perPlatformTags := make([]string, 0, len(b.Platforms))
	baseTag := StableBaseVersionTag(b.Image)
	for _, platform := range b.Platforms {
		platformTag := PlatformSuffixedTag(b.Image.Tag, platform)
		perPlatformTags = append(perPlatformTags, platformTag)
		commands = append(commands, dockerTagTraceCommand(b.ContextDir, FingerprintTag(b.Image, b.Fingerprint, platform), platformTag))
		commands = append(commands, stableBaseVersionTraceCommands(b.ContextDir, platformTag, baseTag, platform)...)
		if b.Push {
			commands = append(commands, helm.CommandSpec{
				Dir:  b.ContextDir,
				Name: "docker",
				Args: dockerPushArgs(platformTag, b.Verbosity),
			})
		}
	}
	if !b.Push {
		return commands
	}
	commands = append(commands, multiPlatformManifestTraceCommands(b.ContextDir, b.Image.Tag, perPlatformTags, b.Image.Insecure)...)
	return commands
}

// multiPlatformManifestTraceCommands mirrors assembleMultiPlatformManifest so the
// dry-run trace stays an honest preview, including the discard of the cached
// manifest list that keeps a re-published version from resolving to the previous
// one, and the --insecure flag a plain-HTTP registry needs on every subcommand
// but rm (which never leaves the local manifest-list store).
func multiPlatformManifestTraceCommands(dir, tag string, perPlatformTags []string, insecure bool) []helm.CommandSpec {
	createArgs := append(DockerManifestArgs("create", insecure), tag)
	createArgs = append(createArgs, perPlatformTags...)
	pushArgs := append(DockerManifestArgs("push", insecure), tag)
	return []helm.CommandSpec{
		{Dir: dir, Name: "docker", Args: []string{"manifest", "rm", tag}},
		{Dir: dir, Name: "docker", Args: createArgs},
		{Dir: dir, Name: "docker", Args: pushArgs},
	}
}

func dockerTagTraceCommand(dir, source, target string) helm.CommandSpec {
	return helm.CommandSpec{
		Dir:  dir,
		Name: "docker",
		Args: []string{"tag", source, target},
	}
}

func (p DockerPushSpec) Command() helm.CommandSpec {
	return helm.CommandSpec{
		Dir:  p.Dir,
		Name: "docker",
		Args: dockerPushArgs(p.Image.Tag, p.Verbosity),
	}
}
