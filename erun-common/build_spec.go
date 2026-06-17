package eruncommon

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

func ResolveDockerImageReference(store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, buildDir string, target DockerCommandTarget) (DockerImageReference, error) {
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

	return resolveDockerImageReferenceForProject(now, projectRoot, environment, buildDir, strings.TrimSpace(target.VersionOverride))
}

func ResolveDockerBuildForComponent(store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, projectRoot, environment, componentName, versionOverride string) (*DockerBuildSpec, error) {
	_, _, resolveBuildContext, now = normalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

	if buildContext, ok := currentComponentDockerBuildContext(resolveBuildContext, componentName); ok {
		build, err := newDockerBuildSpec(now, projectRoot, environment, buildContext, versionOverride)
		if err != nil {
			return nil, err
		}
		return &build, nil
	}

	buildContext, ok, err := FindComponentDockerBuildContext(projectRoot, componentName)
	if err != nil || !ok {
		return nil, err
	}

	build, err := newDockerBuildSpec(now, projectRoot, environment, buildContext, versionOverride)
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

// parseDockerImageReference splits a trimmed image reference into its registry,
// image name, and version. ok is false when the reference is empty, lacks a
// usable name:tag, or carries a registry that looks like a version string.
//
// A version-looking registry (e.g. "1.0.51-snapshot-20260505151841/image-name:tag")
// arises from helm chart printf templates that embed .Chart.AppVersion as a
// namespace prefix; such references are not valid Docker registry hosts and
// would cause a push-time DNS lookup failure, so they are rejected here.
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

func ResolveDockerBuildForImageReference(store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, projectRoot, environment, image string) (DockerBuildSpec, bool, error) {
	image = strings.TrimSpace(image)
	registry, imageName, version, ok := parseDockerImageReference(image)
	if !ok {
		return DockerBuildSpec{}, false, nil
	}

	if registry == "" {
		resolved, err := resolveDockerBuildRegistryForEnvironment(projectRoot, environment)
		if err != nil {
			return DockerBuildSpec{}, false, err
		}
		registry = resolved
	}

	buildContext, ok, err := FindComponentDockerBuildContext(projectRoot, imageName)
	if err != nil || !ok {
		return DockerBuildSpec{}, false, err
	}

	tag := image
	if !strings.Contains(image, "/") {
		tag = fmt.Sprintf("%s/%s:%s", strings.TrimRight(registry, "/"), imageName, version)
	}

	imageRef := DockerImageReference{
		ProjectRoot: projectRoot,
		Environment: strings.TrimSpace(environment),
		Registry:    registry,
		ImageName:   imageName,
		Version:     version,
		Tag:         tag,
	}

	return DockerBuildSpec{
		ContextDir:     ResolveDockerBuildContextDirForProject(buildContext.Dir, projectRoot),
		DockerfilePath: buildContext.DockerfilePath,
		Image:          imageRef,
		Platforms:      slices.Clone(multiPlatformDockerBuilds),
	}, true, nil
}

func resolveDockerBuildSpec(store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, buildContext DockerBuildContext, target DockerCommandTarget) (DockerBuildSpec, error) {
	projectRoot, err := resolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil {
		return DockerBuildSpec{}, err
	}

	environment, err := resolveDockerBuildEnvironment(store, findProjectRoot, projectRoot, target.Environment)
	if err != nil {
		return DockerBuildSpec{}, err
	}

	return newDockerBuildSpec(now, projectRoot, environment, buildContext, strings.TrimSpace(target.VersionOverride))
}

func resolveDockerImageReferenceForProject(now NowFunc, projectRoot, environment, buildDir, versionOverride string) (DockerImageReference, error) {
	registry, err := resolveDockerBuildRegistryForEnvironment(projectRoot, environment)
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

// resolveDockerImageVersion returns (version, baseVersion, versionFromBuildDir, versionFilePath, error).
// version carries a snapshot suffix by default; baseVersion is the stable semver.
//
// build mints the version and no longer keys it off the environment: a snapshot
// (<base>-snapshot-<utc-ts>) by default, the bare base version only when an
// explicit --version/override pins it or a version-from-build-dir file fixes it
// (release passes the resolved release version as the override).
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

func newDockerBuildSpec(now NowFunc, projectRoot, environment string, buildContext DockerBuildContext, versionOverride string) (DockerBuildSpec, error) {
	if strings.TrimSpace(buildContext.DockerfilePath) == "" {
		var err error
		buildContext, err = DockerBuildContextAtDir(buildContext.Dir)
		if err != nil {
			return DockerBuildSpec{}, err
		}
	}

	contextDir := ResolveDockerBuildContextDirForProject(buildContext.Dir, projectRoot)
	imageRef, err := resolveDockerImageReferenceForProject(now, projectRoot, environment, buildContext.Dir, versionOverride)
	if err != nil {
		return DockerBuildSpec{}, err
	}

	// For images whose FROM instruction resolves via ${ERUN_VERSION} (e.g.
	// erun-backend-api, erun-backend-db, erun-mcp), the snapshot version is
	// the right ERUN_VERSION build arg because it matches the locally-built
	// base image tag.  Clear BaseVersion so those images don't try to use the
	// stable semver — that tag doesn't exist in the local Docker cache.
	//
	// Source-compiled images (e.g. erun-devops) use a fixed FROM and bake
	// ERUN_VERSION into the binary via ldflags.  Keeping BaseVersion set on
	// those images ensures the binary is stable across snapshot pushes so
	// Docker can reuse existing registry layers.
	// For local snapshot builds, append "-snapshot" to the stable base version
	// so ERUN_VERSION reads "1.0.51-snapshot" rather than "1.0.51" (which would
	// falsely claim a release).  All images — both source-compiled and wrapper —
	// use this as their ERUN_VERSION build arg.  The build system also emits a
	// stable local tag (e.g. erun-devops:1.0.51-snapshot) so that wrapper images
	// whose FROM references ${ERUN_VERSION} always resolve the same local image,
	// keeping the Docker build cache valid across pushes.  The stable local tag
	// is never pushed; only the timestamped snapshot tag goes to the registry.
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
	}
	if !b.Push {
		return commands
	}
	for _, platformTag := range perPlatformTags {
		commands = append(commands, commandSpec{
			Dir:  b.ContextDir,
			Name: "docker",
			Args: dockerPushArgs(platformTag, b.Verbosity),
		})
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

// stableBaseVersionTraceCommands mirrors tagStableBaseVersionAfterBuild in the
// dry-run plan: a per-arch stable tag (what ${ERUN_VERSION} wrappers resolve)
// followed by the arch-less stable tag, in the same order the executor writes
// them so the trace stays an honest preview of the real build.
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
	}
	if !b.Push {
		return commands
	}
	for _, platformTag := range perPlatformTags {
		commands = append(commands, commandSpec{
			Dir:  b.ContextDir,
			Name: "docker",
			Args: dockerPushArgs(platformTag, b.Verbosity),
		})
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
