package eruncommon

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

func ResolveDockerImageReference(ctx Context, store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, buildDir string, target DockerCommandTarget) (DockerImageReference, error) {
	store, findProjectRoot, _, now = normalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

	target, _, err := ResolveDockerBuildTarget(findProjectRoot, target)
	if err != nil {
		return DockerImageReference{}, err
	}

	projectRoot, err := resolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil {
		return DockerImageReference{}, err
	}

	environment, err := resolveDockerBuildEnvironment(store, findProjectRoot, projectRoot, target.Environment)
	if err != nil {
		return DockerImageReference{}, err
	}

	return resolveDockerImageReferenceForProject(ctx, now, projectRoot, environment, buildDir, strings.TrimSpace(target.VersionOverride))
}

func ResolveDockerBuildForComponent(ctx Context, store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, projectRoot, environment, componentName, versionOverride string) (*DockerBuildSpec, error) {
	_, _, resolveBuildContext, now = normalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

	if buildContext, ok := currentComponentDockerBuildContext(resolveBuildContext, componentName); ok {
		build, err := newDockerBuildSpec(ctx, now, projectRoot, environment, buildContext, versionOverride)
		if err != nil {
			return nil, err
		}
		return &build, nil
	}

	buildContext, ok, err := FindComponentDockerBuildContext(projectRoot, componentName)
	if err != nil || !ok {
		return nil, err
	}

	build, err := newDockerBuildSpec(ctx, now, projectRoot, environment, buildContext, versionOverride)
	if err != nil {
		return nil, err
	}
	return &build, nil
}

func currentComponentDockerBuildContext(resolveBuildContext BuildContextResolverFunc, componentName string) (DockerBuildContext, bool) {
	if resolveBuildContext == nil {
		return DockerBuildContext{}, false
	}
	buildContext, err := resolveBuildContext()
	if err != nil {
		return DockerBuildContext{}, false
	}
	dir := filepath.Clean(strings.TrimSpace(buildContext.Dir))
	if filepath.Base(dir) != strings.TrimSpace(componentName) || filepath.Base(filepath.Dir(dir)) != "docker" {
		return DockerBuildContext{}, false
	}
	return buildContext, strings.TrimSpace(buildContext.DockerfilePath) != ""
}

// parseDockerImageReference rejects a reference whose "registry" is really a
// version string: helm printf templates embed .Chart.AppVersion as a namespace
// prefix (e.g. "1.0.51-snapshot-.../image:tag"), which is not a valid registry
// host and would fail with a push-time DNS lookup.
func parseDockerImageReference(image string) (registry, imageName, version string, ok bool) {
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

func resolveDockerBuildSpec(ctx Context, store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, buildContext DockerBuildContext, target DockerCommandTarget) (DockerBuildSpec, error) {
	projectRoot, err := resolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil {
		return DockerBuildSpec{}, err
	}

	environment, err := resolveDockerBuildEnvironment(store, findProjectRoot, projectRoot, target.Environment)
	if err != nil {
		return DockerBuildSpec{}, err
	}

	return newDockerBuildSpec(ctx, now, projectRoot, environment, buildContext, strings.TrimSpace(target.VersionOverride))
}

func resolveDockerImageReferenceForProject(ctx Context, now NowFunc, projectRoot, environment, buildDir, versionOverride string) (DockerImageReference, error) {
	registry, err := resolveDockerBuildRegistryForEnvironment(ctx, projectRoot, environment)
	if err != nil {
		return DockerImageReference{}, err
	}

	imageName := strings.TrimSpace(filepath.Base(buildDir))
	if imageName == "" || imageName == "." || imageName == string(filepath.Separator) {
		return DockerImageReference{}, fmt.Errorf("could not determine image name from current directory")
	}

	version, baseVersion, versionFromBuildDir, versionFilePath, err := resolveDockerImageVersion(now, projectRoot, buildDir, versionOverride)
	if err != nil {
		return DockerImageReference{}, err
	}

	ref := DockerImageReference{
		ProjectRoot:         projectRoot,
		Environment:         strings.TrimSpace(environment),
		Registry:            registry,
		ImageName:           imageName,
		Version:             version,
		Tag:                 fmt.Sprintf("%s/%s:%s", strings.TrimRight(registry, "/"), imageName, version),
		VersionFilePath:     versionFilePath,
		VersionFromBuildDir: versionFromBuildDir,
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
func resolveDockerImageVersion(now NowFunc, projectRoot, buildDir, versionOverride string) (string, string, bool, string, error) {
	baseVersion, versionFromBuildDir, versionFilePath, err := ResolveDockerBuildVersion(buildDir, projectRoot)
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

func newDockerBuildSpec(ctx Context, now NowFunc, projectRoot, environment string, buildContext DockerBuildContext, versionOverride string) (DockerBuildSpec, error) {
	if strings.TrimSpace(buildContext.DockerfilePath) == "" {
		var err error
		buildContext, err = DockerBuildContextAtDir(buildContext.Dir)
		if err != nil {
			return DockerBuildSpec{}, err
		}
	}

	contextDir, err := ResolveDockerBuildContextDirForProject(buildContext.Dir, projectRoot)
	if err != nil {
		return DockerBuildSpec{}, err
	}
	imageRef, err := resolveDockerImageReferenceForProject(ctx, now, projectRoot, environment, buildContext.Dir, versionOverride)
	if err != nil {
		return DockerBuildSpec{}, err
	}

	// Mark local snapshot builds so ERUN_VERSION reads "1.0.51-snapshot", not
	// "1.0.51", which would falsely claim a release.
	if imageRef.BaseVersion != "" {
		imageRef.BaseVersion = imageRef.BaseVersion + "-snapshot"
	}

	return DockerBuildSpec{
		ContextDir:     contextDir,
		DockerfilePath: buildContext.DockerfilePath,
		Image:          imageRef,
		Platforms:      slices.Clone(multiPlatformDockerBuilds),
	}, nil
}

func (b DockerBuildSpec) traceCommands() []commandSpec {
	if b.Promote {
		return promoteTraceCommands(b)
	}
	return multiPlatformTraceCommands(b)
}

func multiPlatformTraceCommands(b DockerBuildSpec) []commandSpec {
	commands := make([]commandSpec, 0, len(b.Platforms)*4+2)
	perPlatformTags := make([]string, 0, len(b.Platforms))
	baseTag := stableBaseVersionTag(b.Image)
	for _, platform := range b.Platforms {
		platformTag := platformSuffixedTag(b.Image.Tag, platform)
		perPlatformTags = append(perPlatformTags, platformTag)
		commands = append(commands, commandSpec{
			Dir:  b.ContextDir,
			Name: "docker",
			Args: dockerBuildArgs(b, platform),
		})
		if b.Fingerprint != "" {
			commands = append(commands, dockerTagTraceCommand(b.ContextDir, platformTag, fingerprintTag(b.Image, b.Fingerprint, platform)))
		}
		commands = append(commands, stableBaseVersionTraceCommands(b.ContextDir, platformTag, baseTag, platform)...)
		if b.Push {
			commands = append(commands, commandSpec{
				Dir:  b.ContextDir,
				Name: "docker",
				Args: dockerPushArgs(platformTag, b.Verbosity),
			})
		}
	}
	if !b.Push {
		return commands
	}
	commands = append(commands, commandSpec{
		Dir:  b.ContextDir,
		Name: "docker",
		Args: append([]string{"manifest", "create", "--amend", b.Image.Tag}, perPlatformTags...),
	})
	commands = append(commands, commandSpec{
		Dir:  b.ContextDir,
		Name: "docker",
		Args: []string{"manifest", "push", b.Image.Tag},
	})
	return commands
}

// stableBaseVersionTraceCommands must mirror tagStableBaseVersionAfterBuild's
// ordering so the dry-run trace stays an honest preview of the real build.
func stableBaseVersionTraceCommands(dir, platformTag, baseTag, platform string) []commandSpec {
	if baseTag == "" {
		return nil
	}
	commands := make([]commandSpec, 0, 2)
	if archTag := platformSuffixedTag(baseTag, platform); archTag != platformTag {
		commands = append(commands, dockerTagTraceCommand(dir, platformTag, archTag))
	}
	if platformTag != baseTag {
		commands = append(commands, dockerTagTraceCommand(dir, platformTag, baseTag))
	}
	return commands
}

func promoteTraceCommands(b DockerBuildSpec) []commandSpec {
	commands := make([]commandSpec, 0, len(b.Platforms)*3+2)
	perPlatformTags := make([]string, 0, len(b.Platforms))
	baseTag := stableBaseVersionTag(b.Image)
	for _, platform := range b.Platforms {
		platformTag := platformSuffixedTag(b.Image.Tag, platform)
		perPlatformTags = append(perPlatformTags, platformTag)
		commands = append(commands, dockerTagTraceCommand(b.ContextDir, fingerprintTag(b.Image, b.Fingerprint, platform), platformTag))
		commands = append(commands, stableBaseVersionTraceCommands(b.ContextDir, platformTag, baseTag, platform)...)
		if b.Push {
			commands = append(commands, commandSpec{
				Dir:  b.ContextDir,
				Name: "docker",
				Args: dockerPushArgs(platformTag, b.Verbosity),
			})
		}
	}
	if !b.Push {
		return commands
	}
	commands = append(commands, commandSpec{
		Dir:  b.ContextDir,
		Name: "docker",
		Args: append([]string{"manifest", "create", "--amend", b.Image.Tag}, perPlatformTags...),
	})
	commands = append(commands, commandSpec{
		Dir:  b.ContextDir,
		Name: "docker",
		Args: []string{"manifest", "push", b.Image.Tag},
	})
	return commands
}

func dockerTagTraceCommand(dir, source, target string) commandSpec {
	return commandSpec{
		Dir:  dir,
		Name: "docker",
		Args: []string{"tag", source, target},
	}
}

func (p DockerPushSpec) command() commandSpec {
	return commandSpec{
		Dir:  p.Dir,
		Name: "docker",
		Args: dockerPushArgs(p.Image.Tag, p.Verbosity),
	}
}

func NewDockerPushSpec(dir string, image DockerImageReference) DockerPushSpec {
	return DockerPushSpec{Dir: dir, Image: image}
}
