package eruncommon

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

const localSnapshotTimestampFormat = "20060102150405"

const multiPlatformBuildxBuilderName = "erun-multiarch"

var (
	ErrVersionFileNotFound        = errors.New("version file not found for current module")
	ErrDockerBuildContextNotFound = errors.New("dockerfile not found in current directory")
	ErrLinuxPackageBuildNotFound  = errors.New("linux package build script not found in current directory")
	multiPlatformDockerBuilds     = []string{"linux/amd64", "linux/arm64"}
)

type commandSpec struct {
	Dir  string   `json:"dir,omitempty"`
	Name string   `json:"name"`
	Args []string `json:"args"`
}

type (
	BuildContextResolverFunc func() (DockerBuildContext, error)
	NowFunc                  func() time.Time
	DockerImageBuilderFunc   func(DockerBuildSpec, io.Writer, io.Writer) error
	DockerImagePusherFunc    func(string, io.Writer, io.Writer) error
	DockerImageInspectorFunc func(string) (bool, error)
	DockerRegistryLoginFunc  func(string, io.Reader, io.Writer, io.Writer) error
	BuildScriptRunnerFunc    func(string, string, []string, io.Reader, io.Writer, io.Writer) error
	DockerPushFunc           func(Context, DockerPushSpec) error
)

type DockerStore interface {
	ListTenantConfigs() ([]TenantConfig, error)
	LoadTenantConfig(string) (TenantConfig, string, error)
}

type DockerBuildContext struct {
	Dir            string
	DockerfilePath string
}

type DockerImageReference struct {
	ProjectRoot         string
	Environment         string
	Registry            string
	ImageName           string
	Version             string
	Tag                 string
	IsLocalBuild        bool
	VersionFilePath     string
	VersionFromBuildDir bool
}

type DockerBuildSpec struct {
	ContextDir     string
	DockerfilePath string
	Image          DockerImageReference
	Platforms      []string
	Push           bool
	SkipIfExists   bool
}

type DockerPushSpec struct {
	Dir   string
	Image DockerImageReference
}

type scriptSpec struct {
	Dir  string
	Path string
	Env  []string
}

type BuildExecutionSpec struct {
	release      *ReleaseSpec
	script       *scriptSpec
	linuxBuilds  []scriptSpec
	dockerBuilds []DockerBuildSpec
	dockerPushes []DockerPushSpec
	skippedLinux bool
}

type DockerPushExecutionSpec struct {
	builds []DockerBuildSpec
	pushes []DockerPushSpec
}

type DockerCommandTarget struct {
	ProjectRoot     string
	Environment     string
	VersionOverride string
	Release         bool
	Force           bool
	Deploy          bool
}

type DockerRegistryAuthError struct {
	Tag      string
	Registry string
	Message  string
	Err      error
}

type LinuxPackageContext struct {
	Dir               string
	BuildScriptPath   string
	ReleaseScriptPath string
}

func ResolveCurrentDockerBuildSpecs(store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, target DockerCommandTarget) ([]DockerBuildSpec, error) {
	store, findProjectRoot, resolveBuildContext, now = normalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

	buildContexts, err := ResolveCurrentDockerBuildContexts(findProjectRoot, resolveBuildContext, target)
	if err != nil {
		return nil, err
	}

	builds := make([]DockerBuildSpec, 0, len(buildContexts))
	for _, buildContext := range buildContexts {
		build, err := resolveDockerBuildSpec(store, findProjectRoot, resolveBuildContext, now, buildContext, target)
		if err != nil {
			return nil, err
		}
		builds = append(builds, build)
	}

	return builds, nil
}

func ResolveBuildExecution(store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, target DockerCommandTarget) (BuildExecutionSpec, error) {
	store, findProjectRoot, resolveBuildContext, now = normalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

	target, releaseSpec, script, err := resolveBuildExecutionTargetAndScript(findProjectRoot, target)
	if err != nil {
		return BuildExecutionSpec{}, err
	}
	if script != nil {
		script.Env = buildScriptEnv(target.VersionOverride)
		return BuildExecutionSpec{release: releaseSpec, script: script}, nil
	}

	linuxBuilds, hadLinuxBuilds, err := resolveLinuxBuildsForExecution(findProjectRoot, resolveBuildContext, target, releaseSpec)
	if err != nil {
		return BuildExecutionSpec{}, err
	}

	builds, err := ResolveCurrentDockerBuildSpecs(store, findProjectRoot, resolveBuildContext, now, target)
	if err != nil && !errors.Is(err, ErrDockerBuildContextNotFound) {
		return BuildExecutionSpec{}, err
	}

	if buildExecutionHasNoBuilds(linuxBuilds, builds, releaseSpec) {
		return resolveBuildExecutionWithoutBuilds(findProjectRoot, target, hadLinuxBuilds)
	}

	execution := BuildExecutionSpec{linuxBuilds: linuxBuilds, dockerBuilds: builds, skippedLinux: hadLinuxBuilds && len(linuxBuilds) == 0}
	if releaseSpec != nil {
		return BuildExecutionSpecWithRelease(execution, *releaseSpec), nil
	}
	return execution, nil
}

func resolveBuildExecutionTargetAndScript(findProjectRoot ProjectFinderFunc, target DockerCommandTarget) (DockerCommandTarget, *ReleaseSpec, *scriptSpec, error) {
	target, releaseSpec, err := ResolveDockerBuildTarget(findProjectRoot, target)
	if err != nil {
		return DockerCommandTarget{}, nil, nil, err
	}
	script, err := resolveProjectRootBuildScript(findProjectRoot, target)
	if err != nil {
		return DockerCommandTarget{}, nil, nil, err
	}
	return target, releaseSpec, script, nil
}

func buildExecutionHasNoBuilds(linuxBuilds []scriptSpec, builds []DockerBuildSpec, releaseSpec *ReleaseSpec) bool {
	return len(linuxBuilds) == 0 && len(builds) == 0 && releaseSpec == nil
}

func resolveLinuxBuildsForExecution(findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, target DockerCommandTarget, releaseSpec *ReleaseSpec) ([]scriptSpec, bool, error) {
	if releaseSpec != nil {
		return nil, false, nil
	}
	linuxBuilds, err := ResolveCurrentLinuxBuildScripts(findProjectRoot, resolveBuildContext, target, target.VersionOverride)
	if err != nil && !errors.Is(err, ErrLinuxPackageBuildNotFound) {
		return nil, false, err
	}
	hadLinuxBuilds := len(linuxBuilds) > 0
	if hadLinuxBuilds && !LinuxPackageBuildsSupported() {
		return nil, true, nil
	}
	return linuxBuilds, hadLinuxBuilds, nil
}

func resolveBuildExecutionWithoutBuilds(findProjectRoot ProjectFinderFunc, target DockerCommandTarget, hadLinuxBuilds bool) (BuildExecutionSpec, error) {
	if hadLinuxBuilds {
		return BuildExecutionSpec{skippedLinux: true}, nil
	}
	script, err := resolveNestedProjectBuildScript(findProjectRoot, target)
	if err != nil {
		return BuildExecutionSpec{}, err
	}
	if script == nil {
		return BuildExecutionSpec{}, ErrDockerBuildContextNotFound
	}
	script.Env = buildScriptEnv(target.VersionOverride)
	return BuildExecutionSpec{script: script}, nil
}

func BuildExecutionSpecFromDockerBuilds(builds []DockerBuildSpec) BuildExecutionSpec {
	return BuildExecutionSpec{dockerBuilds: builds}
}

func BuildExecutionSpecWithRelease(execution BuildExecutionSpec, release ReleaseSpec) BuildExecutionSpec {
	execution.release = &release
	if len(execution.dockerBuilds) > 0 && len(execution.dockerPushes) == 0 {
		execution.dockerPushes = releaseDockerPushSpecs(execution.dockerBuilds, release.DockerImages)
	}
	if len(execution.dockerBuilds) > 0 && len(execution.dockerPushes) > 0 {
		releaseTags := make(map[string]struct{}, len(execution.dockerPushes))
		for _, pushInput := range execution.dockerPushes {
			releaseTags[strings.TrimSpace(pushInput.Image.Tag)] = struct{}{}
		}
		for i := range execution.dockerBuilds {
			if _, ok := releaseTags[strings.TrimSpace(execution.dockerBuilds[i].Image.Tag)]; !ok {
				continue
			}
			execution.dockerBuilds[i].Platforms = slices.Clone(multiPlatformDockerBuilds)
			execution.dockerBuilds[i].Push = true
		}
	}
	return execution
}

func BuildExecutionUsesBuildScript(execution BuildExecutionSpec) bool {
	return execution.script != nil
}

func releaseDockerPushSpecs(builds []DockerBuildSpec, images []ReleaseDockerImageSpec) []DockerPushSpec {
	if len(builds) == 0 {
		return nil
	}

	releaseTags := make(map[string]struct{}, len(images))
	for _, image := range images {
		releaseTags[strings.TrimSpace(image.Tag)] = struct{}{}
	}
	releaseTags = expandLocalReleaseImageDependencies(builds, releaseTags)

	pushes := make([]DockerPushSpec, 0, len(releaseTags))
	for _, build := range builds {
		if _, ok := releaseTags[strings.TrimSpace(build.Image.Tag)]; !ok {
			continue
		}
		pushes = append(pushes, NewDockerPushSpec(build.ContextDir, build.Image))
	}
	return pushes
}

func expandLocalReleaseImageDependencies(builds []DockerBuildSpec, releaseTags map[string]struct{}) map[string]struct{} {
	if len(builds) == 0 || len(releaseTags) == 0 {
		return releaseTags
	}

	buildsByTag := dockerBuildsByTag(builds)
	expanded, queue := queuedReleaseTags(releaseTags)

	for len(queue) > 0 {
		tag := queue[0]
		queue = queue[1:]

		build, ok := buildsByTag[tag]
		if !ok {
			continue
		}
		for _, dependencyTag := range dockerfileLocalBaseImageTags(build.DockerfilePath, buildsByTag) {
			if _, exists := expanded[dependencyTag]; exists {
				continue
			}
			expanded[dependencyTag] = struct{}{}
			queue = append(queue, dependencyTag)
		}
	}

	for _, build := range builds {
		if !strings.Contains(strings.TrimSpace(build.Image.ImageName), "dind") {
			continue
		}
		expanded[strings.TrimSpace(build.Image.Tag)] = struct{}{}
	}

	return expanded
}

func dockerBuildsByTag(builds []DockerBuildSpec) map[string]DockerBuildSpec {
	buildsByTag := make(map[string]DockerBuildSpec, len(builds))
	for _, build := range builds {
		buildsByTag[strings.TrimSpace(build.Image.Tag)] = build
	}
	return buildsByTag
}

func queuedReleaseTags(releaseTags map[string]struct{}) (map[string]struct{}, []string) {
	expanded := make(map[string]struct{}, len(releaseTags))
	queue := make([]string, 0, len(releaseTags))
	for tag := range releaseTags {
		expanded[tag] = struct{}{}
		queue = append(queue, tag)
	}
	return expanded, queue
}

func ResolveDockerBuildTarget(findProjectRoot ProjectFinderFunc, target DockerCommandTarget) (DockerCommandTarget, *ReleaseSpec, error) {
	target.VersionOverride = strings.TrimSpace(target.VersionOverride)
	if !target.Release {
		return target, nil, nil
	}
	if target.VersionOverride != "" {
		return DockerCommandTarget{}, nil, fmt.Errorf("release build cannot be combined with explicit version override")
	}

	releaseSpec, err := ResolveReleaseSpec(findProjectRoot, ReleaseParams{ProjectRoot: target.ProjectRoot, Force: target.Force})
	if err != nil {
		return DockerCommandTarget{}, nil, err
	}

	target.Release = false
	target.VersionOverride = releaseSpec.Version
	return target, &releaseSpec, nil
}

func DockerPushExecutionSpecFromSpecs(builds []DockerBuildSpec, pushes []DockerPushSpec) DockerPushExecutionSpec {
	return DockerPushExecutionSpec{builds: builds, pushes: pushes}
}

func HasProjectBuildScript(findProjectRoot ProjectFinderFunc, target DockerCommandTarget) (bool, error) {
	script, err := resolveProjectBuildScript(findProjectRoot, target)
	return script != nil, err
}

func ResolveDockerPushExecution(store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, target DockerCommandTarget) (DockerPushExecutionSpec, error) {
	store, findProjectRoot, resolveBuildContext, now = normalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

	buildContexts, err := ResolveCurrentDockerBuildContexts(findProjectRoot, resolveBuildContext, target)
	if err != nil {
		return DockerPushExecutionSpec{}, err
	}

	builds := make([]DockerBuildSpec, 0, len(buildContexts))
	pushes := make([]DockerPushSpec, 0, len(buildContexts))
	for _, buildContext := range buildContexts {
		imageRef, err := ResolveDockerImageReference(store, findProjectRoot, resolveBuildContext, now, buildContext.Dir, target)
		if err != nil {
			return DockerPushExecutionSpec{}, err
		}

		if imageRef.IsLocalBuild {
			build, err := resolveDockerBuildSpec(store, findProjectRoot, resolveBuildContext, now, buildContext, target)
			if err != nil {
				return DockerPushExecutionSpec{}, err
			}
			builds = append(builds, build)
			imageRef = build.Image
		}

		pushes = append(pushes, NewDockerPushSpec(buildContext.Dir, imageRef))
	}

	return DockerPushExecutionSpec{builds: builds, pushes: pushes}, nil
}

func ResolveDockerPushSpec(store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, target DockerCommandTarget) (DockerPushSpec, *DockerBuildSpec, error) {
	store, findProjectRoot, resolveBuildContext, now = normalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

	buildContext, err := resolveBuildContext()
	if err != nil {
		return DockerPushSpec{}, nil, err
	}
	if strings.TrimSpace(buildContext.DockerfilePath) == "" {
		return DockerPushSpec{}, nil, fmt.Errorf("dockerfile not found in current directory")
	}

	imageRef, err := ResolveDockerImageReference(store, findProjectRoot, resolveBuildContext, now, buildContext.Dir, target)
	if err != nil {
		return DockerPushSpec{}, nil, err
	}

	var build *DockerBuildSpec
	if imageRef.IsLocalBuild {
		resolvedBuild, err := resolveDockerBuildSpec(store, findProjectRoot, resolveBuildContext, now, buildContext, target)
		if err != nil {
			return DockerPushSpec{}, nil, err
		}
		build = &resolvedBuild
		imageRef = resolvedBuild.Image
	}

	return NewDockerPushSpec(buildContext.Dir, imageRef), build, nil
}

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

	if !isLocalEnvironment(environment) {
		return nil, nil
	}

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

func ResolveDockerBuildForImageReference(store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, projectRoot, environment, image string) (DockerBuildSpec, bool, error) {
	image = strings.TrimSpace(image)
	if image == "" {
		return DockerBuildSpec{}, false, nil
	}

	nameTag := image
	registry := ""
	if idx := strings.LastIndex(image, "/"); idx >= 0 {
		registry = image[:idx]
		nameTag = image[idx+1:]
	}

	imageName, version, ok := strings.Cut(nameTag, ":")
	if !ok || strings.TrimSpace(imageName) == "" || strings.TrimSpace(version) == "" {
		return DockerBuildSpec{}, false, nil
	}

	buildContext, ok, err := FindComponentDockerBuildContext(projectRoot, imageName)
	if err != nil || !ok {
		return DockerBuildSpec{}, false, err
	}

	imageRef := DockerImageReference{
		ProjectRoot:  projectRoot,
		Environment:  strings.TrimSpace(environment),
		Registry:     registry,
		ImageName:    imageName,
		Version:      version,
		Tag:          image,
		IsLocalBuild: isLocalEnvironment(environment),
	}
	skipIfExists, err := resolveDockerBuildSkipIfExists(projectRoot, environment, imageRef)
	if err != nil {
		return DockerBuildSpec{}, false, err
	}

	return DockerBuildSpec{
		ContextDir:     ResolveDockerBuildContextDirForProject(buildContext.Dir, projectRoot),
		DockerfilePath: buildContext.DockerfilePath,
		Image:          imageRef,
		SkipIfExists:   skipIfExists,
	}, true, nil
}

func resolveProjectBuildScript(findProjectRoot ProjectFinderFunc, target DockerCommandTarget) (*scriptSpec, error) {
	script, err := resolveProjectRootBuildScript(findProjectRoot, target)
	if err != nil || script != nil {
		return script, err
	}
	return resolveNestedProjectBuildScript(findProjectRoot, target)
}

func resolveProjectRootBuildScript(findProjectRoot ProjectFinderFunc, target DockerCommandTarget) (*scriptSpec, error) {
	projectRoot, err := resolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(projectRoot) == "" {
		return nil, nil
	}

	projectRoot = filepath.Clean(projectRoot)
	rootScriptPath := filepath.Join(projectRoot, "build.sh")
	info, err := os.Stat(rootScriptPath)
	if err == nil && !info.IsDir() {
		return &scriptSpec{
			Dir:  projectRoot,
			Path: "./build.sh",
		}, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	return nil, nil
}

func resolveNestedProjectBuildScript(findProjectRoot ProjectFinderFunc, target DockerCommandTarget) (*scriptSpec, error) {
	projectRoot, err := resolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(projectRoot) == "" {
		return nil, nil
	}

	return findNestedProjectBuildScript(filepath.Clean(projectRoot))
}

func findNestedProjectBuildScript(projectRoot string) (*scriptSpec, error) {
	var script *scriptSpec
	err := filepath.WalkDir(projectRoot, func(path string, d fs.DirEntry, err error) error {
		var walkErr error
		script, walkErr = nestedProjectBuildScriptCandidate(projectRoot, path, d, err)
		return walkErr
	})
	if err != nil {
		if errors.Is(err, fs.SkipAll) {
			return script, nil
		}
		return nil, err
	}
	return script, nil
}

func nestedProjectBuildScriptCandidate(projectRoot, path string, d fs.DirEntry, err error) (*scriptSpec, error) {
	if err != nil {
		return nil, err
	}
	if d.IsDir() {
		if d.Name() == ".git" || isProjectBuildArtifactDir(path, projectRoot) {
			return nil, filepath.SkipDir
		}
		return nil, nil
	}
	if d.Name() != "build.sh" || filepath.Dir(path) == projectRoot {
		return nil, nil
	}
	return &scriptSpec{Dir: filepath.Dir(path), Path: "./build.sh"}, fs.SkipAll
}

func isProjectBuildArtifactDir(path, projectRoot string) bool {
	path = filepath.Clean(strings.TrimSpace(path))
	projectRoot = filepath.Clean(strings.TrimSpace(projectRoot))
	if path == "" || projectRoot == "" || path == projectRoot {
		return false
	}

	relative, err := filepath.Rel(projectRoot, path)
	if err != nil {
		return false
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}

	parent := filepath.Base(filepath.Dir(path))
	return parent == "docker" || parent == "linux"
}

func normalizeDockerDependencies(store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc) (DockerStore, ProjectFinderFunc, BuildContextResolverFunc, NowFunc) {
	if store == nil {
		store = ConfigStore{}
	}
	if findProjectRoot == nil {
		findProjectRoot = FindProjectRoot
	}
	if resolveBuildContext == nil {
		resolveBuildContext = ResolveDockerBuildContext
	}
	if now == nil {
		now = time.Now
	}
	return store, findProjectRoot, resolveBuildContext, now
}

func ResolveCurrentDockerBuildContexts(findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, target DockerCommandTarget) ([]DockerBuildContext, error) {
	if resolveBuildContext == nil {
		resolveBuildContext = ResolveDockerBuildContext
	}

	buildContext, err := resolveBuildContext()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(buildContext.DockerfilePath) != "" {
		return []DockerBuildContext{buildContext}, nil
	}

	if buildContexts, err := ResolveDockerBuildContextsAtDir(buildContext.Dir); err == nil {
		return buildContexts, nil
	}

	dockerDir, ok, err := resolveCurrentDevopsDockerDir(findProjectRoot, buildContext.Dir, target)
	if err != nil {
		return nil, err
	}
	if ok {
		return ResolveDockerBuildContextsAtDir(dockerDir)
	}

	return nil, ErrDockerBuildContextNotFound
}

func resolveCurrentDevopsDockerDir(findProjectRoot ProjectFinderFunc, dir string, target DockerCommandTarget) (string, bool, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" {
		return "", false, nil
	}

	dockerDir := filepath.Join(dir, "docker")
	if strings.HasSuffix(filepath.Base(dir), "-devops") {
		if ok, err := isDockerBuildModuleDir(dockerDir); err != nil {
			return "", false, err
		} else if ok {
			return dockerDir, true, nil
		}
	}

	projectRoot, err := resolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil {
		return "", false, err
	}
	if projectRoot == "" || dir != filepath.Clean(projectRoot) {
		return "", false, nil
	}

	return resolveProjectRootDevopsDockerDir(findProjectRoot, projectRoot)
}

func resolveProjectRootDevopsDockerDir(findProjectRoot ProjectFinderFunc, projectRoot string) (string, bool, error) {
	projectRoot = filepath.Clean(strings.TrimSpace(projectRoot))
	if projectRoot == "" {
		return "", false, nil
	}

	dockerDir, ok, err := detectedProjectRootDevopsDockerDir(findProjectRoot, projectRoot)
	if err != nil || ok {
		return dockerDir, ok, err
	}

	candidates, err := findDevopsDockerDirs(projectRoot)
	if err != nil {
		return "", false, err
	}
	switch len(candidates) {
	case 0:
		return "", false, nil
	case 1:
		return candidates[0], true, nil
	default:
		return "", false, fmt.Errorf("multiple devops docker directories found under project root")
	}
}

func detectedProjectRootDevopsDockerDir(findProjectRoot ProjectFinderFunc, projectRoot string) (string, bool, error) {
	tenant, detectedProjectRoot, err := findProjectRoot()
	if err != nil || filepath.Clean(strings.TrimSpace(detectedProjectRoot)) != projectRoot || strings.TrimSpace(tenant) == "" {
		return "", false, nil
	}
	dockerDir := filepath.Join(projectRoot, RuntimeReleaseName(tenant), "docker")
	if ok, err := isDockerBuildModuleDir(dockerDir); err != nil {
		return "", false, err
	} else if ok {
		return dockerDir, true, nil
	}
	return "", false, nil
}

func findDevopsDockerDirs(projectRoot string) ([]string, error) {
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		return nil, err
	}
	candidates := make([]string, 0, 1)
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasSuffix(entry.Name(), "-devops") {
			continue
		}
		dockerDir := filepath.Join(projectRoot, entry.Name(), "docker")
		ok, err := isDockerBuildModuleDir(dockerDir)
		if err != nil {
			return nil, err
		}
		if ok {
			candidates = append(candidates, dockerDir)
		}
	}
	return candidates, nil
}

func isDockerBuildModuleDir(dir string) (bool, error) {
	buildContexts, err := ResolveDockerBuildContextsAtDir(dir)
	if err != nil {
		if errors.Is(err, ErrDockerBuildContextNotFound) {
			return false, nil
		}
		return false, err
	}
	return len(buildContexts) > 0, nil
}

func resolveDockerBuildProjectRoot(findProjectRoot ProjectFinderFunc, target DockerCommandTarget) (string, error) {
	if projectRoot := strings.TrimSpace(target.ProjectRoot); projectRoot != "" {
		return filepath.Clean(projectRoot), nil
	}

	_, projectRoot, err := findProjectRoot()
	if err != nil {
		if errors.Is(err, ErrNotInGitRepository) {
			return "", nil
		}
		return "", err
	}
	return projectRoot, nil
}

func resolveDockerBuildEnvironment(store DockerStore, findProjectRoot ProjectFinderFunc, projectRoot, environment string) (string, error) {
	if environment = strings.TrimSpace(environment); environment != "" {
		return environment, nil
	}

	cleanProjectRoot := filepath.Clean(projectRoot)
	if environment, err := dockerBuildEnvironmentFromTenantConfigs(store, cleanProjectRoot); environment != "" || err != nil {
		return environment, err
	}
	return dockerBuildEnvironmentFromDetectedProject(store, findProjectRoot, cleanProjectRoot)
}

func dockerBuildEnvironmentFromTenantConfigs(store DockerStore, cleanProjectRoot string) (string, error) {
	tenants, err := store.ListTenantConfigs()
	if err != nil {
		if errors.Is(err, ErrNotInitialized) {
			return "", nil
		}
		return "", err
	}

	for _, tenantConfig := range tenants {
		if filepath.Clean(tenantConfig.ProjectRoot) != cleanProjectRoot {
			continue
		}
		return strings.TrimSpace(tenantConfig.DefaultEnvironment), nil
	}
	return "", nil
}

func dockerBuildEnvironmentFromDetectedProject(store DockerStore, findProjectRoot ProjectFinderFunc, cleanProjectRoot string) (string, error) {
	tenant, detectedProjectRoot, err := findProjectRoot()
	if err != nil {
		if errors.Is(err, ErrNotInGitRepository) {
			return "", nil
		}
		return "", err
	}
	if filepath.Clean(detectedProjectRoot) != cleanProjectRoot || strings.TrimSpace(tenant) == "" {
		return "", nil
	}

	tenantConfig, _, err := store.LoadTenantConfig(tenant)
	if err != nil {
		if errors.Is(err, ErrNotInitialized) {
			return "", nil
		}
		return "", err
	}
	if projectRoot := strings.TrimSpace(tenantConfig.ProjectRoot); projectRoot != "" && filepath.Clean(projectRoot) != cleanProjectRoot {
		return "", nil
	}

	return strings.TrimSpace(tenantConfig.DefaultEnvironment), nil
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

	version, versionFromBuildDir, versionFilePath, err := resolveDockerImageVersion(now, projectRoot, environment, buildDir, versionOverride)
	if err != nil {
		return DockerImageReference{}, err
	}

	return DockerImageReference{
		ProjectRoot:         projectRoot,
		Environment:         strings.TrimSpace(environment),
		Registry:            registry,
		ImageName:           imageName,
		Version:             version,
		Tag:                 fmt.Sprintf("%s/%s:%s", strings.TrimRight(registry, "/"), imageName, version),
		IsLocalBuild:        isLocalEnvironment(environment),
		VersionFilePath:     versionFilePath,
		VersionFromBuildDir: versionFromBuildDir,
	}, nil
}

func resolveDockerImageVersion(now NowFunc, projectRoot, environment, buildDir, versionOverride string) (string, bool, string, error) {
	baseVersion, versionFromBuildDir, versionFilePath, err := ResolveDockerBuildVersion(buildDir, projectRoot)
	if err != nil {
		return "", false, "", err
	}

	if versionOverride = strings.TrimSpace(versionOverride); versionOverride != "" {
		if versionFromBuildDir {
			return baseVersion, versionFromBuildDir, versionFilePath, nil
		}
		return versionOverride, versionFromBuildDir, versionFilePath, nil
	}

	if !isLocalEnvironment(environment) || versionFromBuildDir {
		return baseVersion, versionFromBuildDir, versionFilePath, nil
	}
	return formatLocalSnapshotVersion(baseVersion, now()), versionFromBuildDir, versionFilePath, nil
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
	skipIfExists, err := resolveDockerBuildSkipIfExists(projectRoot, environment, imageRef)
	if err != nil {
		return DockerBuildSpec{}, err
	}

	return DockerBuildSpec{
		ContextDir:     contextDir,
		DockerfilePath: buildContext.DockerfilePath,
		Image:          imageRef,
		SkipIfExists:   skipIfExists,
	}, nil
}

func (b DockerBuildSpec) command() commandSpec {
	args := dockerBuildArgs(b)
	return commandSpec{
		Dir:  b.ContextDir,
		Name: "docker",
		Args: args,
	}
}

func (b DockerBuildSpec) traceCommands() []commandSpec {
	if len(b.Platforms) == 0 {
		return []commandSpec{b.command()}
	}

	return append(dockerBuildxSetupCommands(b.ContextDir), b.command())
}

func (p DockerPushSpec) command() commandSpec {
	return commandSpec{
		Dir:  p.Dir,
		Name: "docker",
		Args: []string{"push", p.Image.Tag},
	}
}

func NewDockerPushSpec(dir string, image DockerImageReference) DockerPushSpec {
	return DockerPushSpec{Dir: dir, Image: image}
}

func RunDockerBuild(ctx Context, buildInput DockerBuildSpec, build DockerImageBuilderFunc) error {
	return runDockerBuild(ctx, buildInput, build, nil)
}

func runDockerBuild(ctx Context, buildInput DockerBuildSpec, build DockerImageBuilderFunc, inspect DockerImageInspectorFunc) error {
	if build == nil {
		build = DockerImageBuilder
	}
	skip, err := shouldSkipDockerBuild(ctx, buildInput, inspect)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}
	for _, command := range buildInput.traceCommands() {
		ctx.TraceCommand(command.Dir, command.Name, command.Args...)
	}
	if ctx.DryRun {
		return nil
	}
	return build(buildInput, ctx.Stdout, ctx.Stderr)
}

func shouldSkipDockerBuild(ctx Context, buildInput DockerBuildSpec, inspect DockerImageInspectorFunc) (bool, error) {
	if !buildInput.SkipIfExists {
		return false, nil
	}
	tag := strings.TrimSpace(buildInput.Image.Tag)
	if tag == "" {
		return false, nil
	}
	inspectCommand := []string{"image", "inspect", tag}
	if inspect == nil {
		inspect = DockerImageExists
		if buildInput.Push {
			inspect = DockerManifestExists
			inspectCommand = []string{"manifest", "inspect", tag}
		}
	}

	ctx.TraceCommand("", "docker", inspectCommand...)
	exists, err := inspect(tag)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	ctx.Trace("skipping docker build because configured image exists: " + tag)
	return true, nil
}

func RunDockerBuilds(ctx Context, builds []DockerBuildSpec, build DockerImageBuilderFunc) error {
	return runDockerBuilds(ctx, builds, build, nil)
}

func runDockerBuilds(ctx Context, builds []DockerBuildSpec, build DockerImageBuilderFunc, inspect DockerImageInspectorFunc) error {
	for _, buildInput := range orderedDockerBuildSpecs(builds) {
		if err := runDockerBuild(ctx, buildInput, build, inspect); err != nil {
			return err
		}
	}
	return nil
}

func RunBuildExecution(ctx Context, execution BuildExecutionSpec, runScript BuildScriptRunnerFunc, build DockerImageBuilderFunc, push DockerPushFunc) error {
	return runBuildExecution(ctx, execution, nil, runScript, build, push, nil)
}

func RunBuildExecutionAndDeploy(ctx Context, execution BuildExecutionSpec, deploySpecs []DeploySpec, runScript BuildScriptRunnerFunc, build DockerImageBuilderFunc, push DockerPushFunc, deploy HelmChartDeployerFunc) error {
	return runBuildExecution(ctx, execution, deploySpecs, runScript, build, push, deploy)
}

func runBuildExecution(ctx Context, execution BuildExecutionSpec, deploySpecs []DeploySpec, runScript BuildScriptRunnerFunc, build DockerImageBuilderFunc, push DockerPushFunc, deploy HelmChartDeployerFunc) error {
	if execution.release != nil {
		if err := RunReleaseSpec(ctx, *execution.release, nil, runScript); err != nil {
			return err
		}
	}
	if execution.skippedLinux {
		ctx.Trace("skipping linux package scripts: host is not Linux or dpkg-deb is unavailable")
	}

	pushedTags, err := runBuildExecutionBuilds(ctx, execution, deploySpecs, runScript, build, push)
	if err != nil {
		return err
	}
	for _, deploySpec := range filterDeploySpecsForPushedTags(deploySpecs, pushedTags) {
		if err := RunDeploySpec(ctx, deploySpec, build, push, deploy); err != nil {
			return err
		}
	}
	if execution.release != nil {
		ctx.Info("release version: " + execution.release.Version)
	}
	if version := deployedVersionForSpecs(deploySpecs); version != "" {
		ctx.Info("deployed version: " + version)
	}
	return nil
}

func runBuildExecutionBuilds(ctx Context, execution BuildExecutionSpec, deploySpecs []DeploySpec, runScript BuildScriptRunnerFunc, build DockerImageBuilderFunc, push DockerPushFunc) (map[string]struct{}, error) {
	pushedTags := make(map[string]struct{}, len(execution.dockerBuilds)+len(execution.dockerPushes))
	if execution.script != nil {
		if len(deploySpecs) > 0 {
			return nil, fmt.Errorf("build deploy is not supported for project build scripts")
		}
		return pushedTags, runScriptSpec(ctx, *execution.script, runScript)
	}
	if err := runScriptSpecs(ctx, execution.linuxBuilds, runScript); err != nil {
		return nil, err
	}
	return runDockerBuildExecutionPhase(ctx, execution, deploySpecs, build, push, pushedTags)
}

func runDockerBuildExecutionPhase(ctx Context, execution BuildExecutionSpec, deploySpecs []DeploySpec, build DockerImageBuilderFunc, push DockerPushFunc, pushedTags map[string]struct{}) (map[string]struct{}, error) {
	if len(execution.dockerPushes) > 0 {
		err := RunDockerPushExecution(ctx, DockerPushExecutionSpec{builds: execution.dockerBuilds, pushes: execution.dockerPushes}, build, push)
		if err != nil {
			return pushedTags, err
		}
		return recordDockerPushTags(pushedTags, execution.dockerPushes), nil
	}
	if len(deploySpecs) > 0 {
		return pushedTags, buildAndPushDeployDockerImages(ctx, execution.dockerBuilds, build, push, pushedTags)
	}
	return pushedTags, RunDockerBuilds(ctx, execution.dockerBuilds, build)
}

func recordDockerPushTags(tags map[string]struct{}, pushes []DockerPushSpec) map[string]struct{} {
	for _, pushInput := range pushes {
		tags[pushInput.Image.Tag] = struct{}{}
	}
	return tags
}

func buildAndPushDeployDockerImages(ctx Context, builds []DockerBuildSpec, build DockerImageBuilderFunc, push DockerPushFunc, pushedTags map[string]struct{}) error {
	if err := RunDockerBuilds(ctx, builds, build); err != nil {
		return err
	}
	for _, buildInput := range builds {
		pushInput := NewDockerPushSpec(buildInput.ContextDir, buildInput.Image)
		if err := RunDockerPushSpec(ctx, pushInput, nil, build, push); err != nil {
			return err
		}
		pushedTags[pushInput.Image.Tag] = struct{}{}
	}
	return nil
}

func filterDeploySpecsForPushedTags(specs []DeploySpec, pushedTags map[string]struct{}) []DeploySpec {
	if len(specs) == 0 || len(pushedTags) == 0 {
		return specs
	}

	filtered := make([]DeploySpec, 0, len(specs))
	for _, spec := range specs {
		copySpec := spec
		copySpec.Builds = filterDockerBuildsForPushedTags(spec.Builds, pushedTags)
		filtered = append(filtered, copySpec)
	}
	return filtered
}

func filterDockerBuildsForPushedTags(builds []DockerBuildSpec, pushedTags map[string]struct{}) []DockerBuildSpec {
	if len(builds) == 0 || len(pushedTags) == 0 {
		return builds
	}

	filtered := make([]DockerBuildSpec, 0, len(builds))
	for _, build := range builds {
		if _, ok := pushedTags[build.Image.Tag]; ok {
			continue
		}
		filtered = append(filtered, build)
	}
	return filtered
}

func deployedVersionForSpecs(specs []DeploySpec) string {
	version := ""
	for _, spec := range specs {
		current := strings.TrimSpace(spec.Deploy.Version)
		if current == "" {
			return ""
		}
		if version == "" {
			version = current
			continue
		}
		if current != version {
			return ""
		}
	}
	return version
}

func RunDockerPush(ctx Context, pushInput DockerPushSpec, push DockerImagePusherFunc) error {
	if push == nil {
		push = DockerImagePusher
	}
	command := pushInput.command()
	ctx.TraceCommand(command.Dir, command.Name, command.Args...)
	if ctx.DryRun {
		return nil
	}
	return push(pushInput.Image.Tag, ctx.Stdout, ctx.Stderr)
}

func RunDockerPushSpec(ctx Context, pushInput DockerPushSpec, buildInput *DockerBuildSpec, build DockerImageBuilderFunc, push DockerPushFunc) error {
	if buildInput != nil {
		if err := runDockerBuild(ctx, *buildInput, build, nil); err != nil {
			return err
		}
		if buildInput.Push {
			return nil
		}
	}
	if push == nil {
		push = func(ctx Context, pushInput DockerPushSpec) error {
			return RunDockerPush(ctx, pushInput, nil)
		}
	}
	return push(ctx, pushInput)
}

func RunDockerPushExecution(ctx Context, execution DockerPushExecutionSpec, build DockerImageBuilderFunc, push DockerPushFunc) error {
	if err := RunDockerBuilds(ctx, execution.builds, build); err != nil {
		return err
	}
	builtAndPushedTags := make(map[string]struct{}, len(execution.builds))
	for _, buildInput := range execution.builds {
		if !buildInput.Push {
			continue
		}
		builtAndPushedTags[buildInput.Image.Tag] = struct{}{}
	}
	for _, pushInput := range execution.pushes {
		if _, ok := builtAndPushedTags[pushInput.Image.Tag]; ok {
			continue
		}
		if err := RunDockerPushSpec(ctx, pushInput, nil, build, push); err != nil {
			return err
		}
	}
	return nil
}

func ResolveDockerBuildContext() (DockerBuildContext, error) {
	dir, err := os.Getwd()
	if err != nil {
		return DockerBuildContext{}, err
	}
	return DockerBuildContextAtDir(dir)
}

func DockerBuildContextAtDir(dir string) (DockerBuildContext, error) {
	dockerfilePath := filepath.Join(dir, "Dockerfile")
	info, err := os.Stat(dockerfilePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DockerBuildContext{Dir: dir}, nil
		}
		return DockerBuildContext{}, err
	}
	if info.IsDir() {
		return DockerBuildContext{Dir: dir}, nil
	}
	return DockerBuildContext{Dir: dir, DockerfilePath: dockerfilePath}, nil
}

func orderedDockerBuildSpecs(builds []DockerBuildSpec) []DockerBuildSpec {
	if len(builds) < 2 {
		return builds
	}

	buildsByTag := make(map[string]DockerBuildSpec, len(builds))
	orderIndex := make(map[string]int, len(builds))
	for i, build := range builds {
		tag := strings.TrimSpace(build.Image.Tag)
		buildsByTag[tag] = build
		orderIndex[tag] = i
	}

	tags := make([]string, 0, len(builds))
	seen := make(map[string]bool, len(builds))
	var visit func(string)
	visit = func(tag string) {
		if seen[tag] {
			return
		}
		seen[tag] = true
		build, ok := buildsByTag[tag]
		if ok {
			for _, dependencyTag := range dockerfileLocalBaseImageTags(build.DockerfilePath, buildsByTag) {
				visit(dependencyTag)
			}
		}
		tags = append(tags, tag)
	}

	inputTags := make([]string, 0, len(builds))
	for _, build := range builds {
		inputTags = append(inputTags, strings.TrimSpace(build.Image.Tag))
	}
	slices.SortStableFunc(inputTags, func(a, b string) int {
		return orderIndex[a] - orderIndex[b]
	})
	for _, tag := range inputTags {
		visit(tag)
	}

	ordered := make([]DockerBuildSpec, 0, len(builds))
	for _, tag := range tags {
		ordered = append(ordered, buildsByTag[tag])
	}
	return ordered
}

var dockerfileFromPattern = regexp.MustCompile(`(?im)^\s*FROM(?:\s+--platform=\S+)?\s+([^\s]+)`)

func dockerfileLocalBaseImageTags(dockerfilePath string, buildsByTag map[string]DockerBuildSpec) []string {
	data, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return nil
	}

	matches := dockerfileFromPattern.FindAllStringSubmatch(string(data), -1)
	dependencies := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		imageRef := strings.TrimSpace(match[1])
		if imageRef == "" || strings.HasPrefix(imageRef, "${") {
			continue
		}
		if _, ok := buildsByTag[imageRef]; !ok {
			continue
		}
		dependencies = append(dependencies, imageRef)
	}
	return dependencies
}

func ResolveDockerBuildContextsAtDir(dir string) ([]DockerBuildContext, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || filepath.Base(dir) != "docker" {
		return nil, ErrDockerBuildContextNotFound
	}

	buildContexts, err := DockerBuildContextsUnderDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrDockerBuildContextNotFound
		}
		return nil, err
	}
	if len(buildContexts) == 0 {
		return nil, ErrDockerBuildContextNotFound
	}

	return buildContexts, nil
}

func DockerBuildContextsUnderDir(dir string) ([]DockerBuildContext, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	buildContexts := make([]DockerBuildContext, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		buildContext, err := DockerBuildContextAtDir(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(buildContext.DockerfilePath) == "" {
			continue
		}

		buildContexts = append(buildContexts, buildContext)
	}

	return buildContexts, nil
}

func FindComponentDockerBuildContext(projectRoot, componentName string) (DockerBuildContext, bool, error) {
	projectRoot = filepath.Clean(strings.TrimSpace(projectRoot))
	componentName = strings.TrimSpace(componentName)
	if projectRoot == "" || componentName == "" {
		return DockerBuildContext{}, false, nil
	}

	matches := make([]DockerBuildContext, 0, 1)
	err := filepath.WalkDir(projectRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "Dockerfile" {
			return nil
		}

		dir := filepath.Dir(path)
		if filepath.Base(dir) != componentName || filepath.Base(filepath.Dir(dir)) != "docker" {
			return nil
		}

		matches = append(matches, DockerBuildContext{
			Dir:            dir,
			DockerfilePath: path,
		})
		return nil
	})
	if err != nil {
		return DockerBuildContext{}, false, err
	}
	if len(matches) == 0 {
		return DockerBuildContext{}, false, nil
	}
	if len(matches) > 1 {
		return DockerBuildContext{}, false, fmt.Errorf("multiple Docker build contexts found for component %q", componentName)
	}
	return matches[0], true, nil
}

func DockerImageBuilder(buildInput DockerBuildSpec, stdout, stderr io.Writer) error {
	if len(buildInput.Platforms) > 0 {
		if err := ensureDockerBuildxBuilder(buildInput.ContextDir, buildInput.Platforms, stdout, stderr); err != nil {
			return err
		}
	}
	cmd := exec.Command("docker", dockerBuildArgs(buildInput)...)
	cmd.Dir = buildInput.ContextDir
	output := new(bytes.Buffer)
	cmd.Stdout = dockerCommandOutputWriter(stdout, output)
	cmd.Stderr = dockerCommandOutputWriter(stderr, output)
	err := cmd.Run()
	if err == nil {
		return nil
	}

	message := output.String()
	if buildInput.Push && IsDockerPushAuthorizationError(message) {
		return DockerRegistryAuthError{
			Tag:      buildInput.Image.Tag,
			Registry: dockerRegistryFromImageTag(buildInput.Image.Tag),
			Message:  strings.TrimSpace(message),
			Err:      err,
		}
	}

	return err
}

func DockerImageExists(tag string) (bool, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return false, nil
	}
	cmd := exec.Command("docker", "image", "inspect", tag)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false, nil
	}
	return false, err
}

func DockerManifestExists(tag string) (bool, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return false, nil
	}
	cmd := exec.Command("docker", "manifest", "inspect", tag)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false, nil
	}
	return false, err
}

func dockerCommandOutputWriter(primary io.Writer, capture io.Writer) io.Writer {
	writers := make([]io.Writer, 0, 2)
	if primary != nil {
		writers = append(writers, primary)
	}
	if capture != nil {
		writers = append(writers, capture)
	}
	if len(writers) == 0 {
		return io.Discard
	}
	if len(writers) == 1 {
		return writers[0]
	}
	return io.MultiWriter(writers...)
}

func dockerBuildArgs(buildInput DockerBuildSpec) []string {
	tag := strings.TrimSpace(buildInput.Image.Tag)
	args := []string{"build"}
	if len(buildInput.Platforms) > 0 {
		args = []string{"buildx", "build", "--builder", multiPlatformBuildxBuilderName, "--platform", strings.Join(buildInput.Platforms, ",")}
	}
	args = append(args, "-t", tag)
	if version := dockerImageTagVersion(tag); version != "" {
		args = append(args, "--build-arg", "ERUN_VERSION="+version)
	}
	if buildInput.Push {
		args = append(args, "--push")
	}
	args = append(args, "-f", buildInput.DockerfilePath, ".")
	return args
}

func dockerBuildxSetupCommands(dir string) []commandSpec {
	return []commandSpec{
		{
			Dir:  dir,
			Name: "docker",
			Args: []string{"buildx", "inspect", multiPlatformBuildxBuilderName},
		},
		{
			Dir:  dir,
			Name: "docker",
			Args: []string{"buildx", "create", "--name", multiPlatformBuildxBuilderName, "--driver", "docker-container"},
		},
		{
			Dir:  dir,
			Name: "docker",
			Args: []string{"buildx", "inspect", "--builder", multiPlatformBuildxBuilderName, "--bootstrap"},
		},
	}
}

var buildxPlatformsPattern = regexp.MustCompile(`(?m)^\s*Platforms:\s*(.+)$`)

func ensureDockerBuildxBuilder(dir string, requiredPlatforms []string, stdout, stderr io.Writer) error {
	inspect := exec.Command("docker", "buildx", "inspect", multiPlatformBuildxBuilderName)
	inspect.Dir = dir
	inspect.Stdout = io.Discard
	inspect.Stderr = io.Discard
	if err := inspect.Run(); err != nil {
		create := exec.Command("docker", "buildx", "create", "--name", multiPlatformBuildxBuilderName, "--driver", "docker-container")
		create.Dir = dir
		create.Stdout = stdout
		create.Stderr = stderr
		if err := create.Run(); err != nil {
			return err
		}
	}

	bootstrap := exec.Command("docker", "buildx", "inspect", "--builder", multiPlatformBuildxBuilderName, "--bootstrap")
	bootstrap.Dir = dir
	output := new(bytes.Buffer)
	bootstrap.Stdout = io.MultiWriter(stdout, output)
	bootstrap.Stderr = io.MultiWriter(stderr, output)
	if err := bootstrap.Run(); err != nil {
		return err
	}
	if missingPlatforms := missingBuildxPlatforms(output.String(), requiredPlatforms); len(missingPlatforms) > 0 {
		availablePlatforms := buildxPlatforms(output.String())
		if len(availablePlatforms) == 0 {
			return fmt.Errorf("multi-platform release builder %q did not report supported platforms after bootstrap", multiPlatformBuildxBuilderName)
		}
		return fmt.Errorf("multi-platform release builder %q does not support required platforms: %s (available: %s)", multiPlatformBuildxBuilderName, strings.Join(missingPlatforms, ", "), strings.Join(availablePlatforms, ", "))
	}
	return nil
}

func buildxPlatforms(output string) []string {
	match := buildxPlatformsPattern.FindStringSubmatch(output)
	if len(match) < 2 {
		return nil
	}
	rawPlatforms := strings.Split(match[1], ",")
	platforms := make([]string, 0, len(rawPlatforms))
	for _, platform := range rawPlatforms {
		platform = strings.TrimSpace(platform)
		if platform == "" {
			continue
		}
		platforms = append(platforms, platform)
	}
	return platforms
}

func missingBuildxPlatforms(output string, requiredPlatforms []string) []string {
	if len(requiredPlatforms) == 0 {
		return nil
	}
	supported := make(map[string]struct{}, len(requiredPlatforms))
	for _, platform := range buildxPlatforms(output) {
		supported[platform] = struct{}{}
	}
	missing := make([]string, 0, len(requiredPlatforms))
	for _, platform := range requiredPlatforms {
		platform = strings.TrimSpace(platform)
		if platform == "" {
			continue
		}
		if _, ok := supported[platform]; ok {
			continue
		}
		missing = append(missing, platform)
	}
	return missing
}

func dockerImageTagVersion(tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return ""
	}
	index := strings.LastIndex(tag, ":")
	if index < 0 || index == len(tag)-1 {
		return ""
	}
	return tag[index+1:]
}

func BuildScriptRunner(dir, scriptPath string, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := exec.Command(scriptPath)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func DockerImagePusher(tag string, stdout, stderr io.Writer) error {
	pushCmd := exec.Command("docker", "push", tag)
	output := new(bytes.Buffer)
	pushCmd.Stdout = dockerCommandOutputWriter(stdout, output)
	pushCmd.Stderr = dockerCommandOutputWriter(stderr, output)
	err := pushCmd.Run()
	if err == nil {
		return nil
	}

	message := output.String()
	if IsDockerPushAuthorizationError(message) {
		return DockerRegistryAuthError{
			Tag:      tag,
			Registry: dockerRegistryFromImageTag(tag),
			Message:  strings.TrimSpace(message),
			Err:      err,
		}
	}

	return err
}

func DockerRegistryLogin(registry string, stdin io.Reader, stdout, stderr io.Writer) error {
	args := []string{"login"}
	if registry != "" {
		args = append(args, registry)
	}

	loginCmd := exec.Command("docker", args...)
	loginCmd.Stdin = stdin
	loginCmd.Stdout = stdout
	loginCmd.Stderr = stderr
	return loginCmd.Run()
}

func runScriptSpec(ctx Context, script scriptSpec, run BuildScriptRunnerFunc) error {
	if run == nil {
		run = BuildScriptRunner
	}
	name, args := scriptTraceCommand(script)
	ctx.TraceCommand(script.Dir, name, args...)
	if ctx.DryRun {
		return nil
	}
	return run(script.Dir, script.Path, script.Env, ctx.Stdin, ctx.Stdout, ctx.Stderr)
}

func runScriptSpecs(ctx Context, scripts []scriptSpec, run BuildScriptRunnerFunc) error {
	for _, script := range scripts {
		if err := runScriptSpec(ctx, script, run); err != nil {
			return err
		}
	}
	return nil
}

func buildScriptEnv(version string) []string {
	version = strings.TrimSpace(version)
	if version == "" {
		return nil
	}
	return []string{"ERUN_BUILD_VERSION=" + version}
}

func scriptTraceCommand(script scriptSpec) (string, []string) {
	if len(script.Env) == 0 {
		return script.Path, nil
	}

	args := append([]string{}, script.Env...)
	args = append(args, script.Path)
	return args[0], args[1:]
}

func ResolveCurrentLinuxBuildScripts(findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, target DockerCommandTarget, version string) ([]scriptSpec, error) {
	contexts, err := ResolveCurrentLinuxPackageContexts(findProjectRoot, resolveBuildContext, target)
	if err != nil {
		return nil, err
	}

	scripts := make([]scriptSpec, 0, len(contexts))
	for _, context := range contexts {
		if strings.TrimSpace(context.BuildScriptPath) == "" {
			continue
		}
		scripts = append(scripts, newScriptSpec(context.Dir, "./build.sh", version))
	}
	if len(scripts) == 0 {
		return nil, ErrLinuxPackageBuildNotFound
	}
	return scripts, nil
}

func ResolveCurrentLinuxReleaseScripts(findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, target DockerCommandTarget, version string) ([]scriptSpec, error) {
	contexts, err := ResolveCurrentLinuxPackageContexts(findProjectRoot, resolveBuildContext, target)
	if err != nil {
		return nil, err
	}

	scripts := make([]scriptSpec, 0, len(contexts))
	for _, context := range contexts {
		if strings.TrimSpace(context.ReleaseScriptPath) == "" {
			continue
		}
		scripts = append(scripts, newScriptSpec(context.Dir, "./release.sh", version))
	}
	if len(scripts) == 0 {
		return nil, ErrLinuxPackageBuildNotFound
	}
	return scripts, nil
}

func ResolveCurrentLinuxPackageContexts(findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, target DockerCommandTarget) ([]LinuxPackageContext, error) {
	if resolveBuildContext == nil {
		resolveBuildContext = ResolveDockerBuildContext
	}

	buildContext, err := resolveBuildContext()
	if err != nil {
		return nil, err
	}

	if context, ok, err := LinuxPackageContextAtDir(buildContext.Dir); err != nil {
		return nil, err
	} else if ok {
		return []LinuxPackageContext{context}, nil
	}

	if contexts, err := ResolveLinuxPackageContextsAtDir(buildContext.Dir); err == nil {
		return contexts, nil
	} else if !errors.Is(err, ErrLinuxPackageBuildNotFound) {
		return nil, err
	}

	linuxDir, ok, err := resolveCurrentDevopsLinuxDir(findProjectRoot, buildContext.Dir, target)
	if err != nil {
		return nil, err
	}
	if ok {
		return ResolveLinuxPackageContextsAtDir(linuxDir)
	}

	return nil, ErrLinuxPackageBuildNotFound
}

func LinuxPackageContextAtDir(dir string) (LinuxPackageContext, bool, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" {
		return LinuxPackageContext{}, false, nil
	}
	if filepath.Base(filepath.Dir(dir)) != "linux" {
		return LinuxPackageContext{}, false, nil
	}

	buildScriptPath, buildFound, err := linuxPackageScriptPath(dir, "build.sh")
	if err != nil {
		return LinuxPackageContext{}, false, err
	}
	releaseScriptPath, releaseFound, err := linuxPackageScriptPath(dir, "release.sh")
	if err != nil {
		return LinuxPackageContext{}, false, err
	}
	if !buildFound && !releaseFound {
		return LinuxPackageContext{}, false, nil
	}

	return LinuxPackageContext{
		Dir:               dir,
		BuildScriptPath:   buildScriptPath,
		ReleaseScriptPath: releaseScriptPath,
	}, true, nil
}

func ResolveLinuxPackageContextsAtDir(dir string) ([]LinuxPackageContext, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || filepath.Base(dir) != "linux" {
		return nil, ErrLinuxPackageBuildNotFound
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrLinuxPackageBuildNotFound
		}
		return nil, err
	}

	contexts := make([]LinuxPackageContext, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		context, ok, err := LinuxPackageContextAtDir(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		contexts = append(contexts, context)
	}
	if len(contexts) == 0 {
		return nil, ErrLinuxPackageBuildNotFound
	}
	return contexts, nil
}

func FindComponentLinuxPackageContext(projectRoot, componentName string) (LinuxPackageContext, bool, error) {
	projectRoot = filepath.Clean(strings.TrimSpace(projectRoot))
	componentName = strings.TrimSpace(componentName)
	if projectRoot == "" || componentName == "" {
		return LinuxPackageContext{}, false, nil
	}

	matches := make([]LinuxPackageContext, 0, 1)
	err := filepath.WalkDir(projectRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "build.sh" && d.Name() != "release.sh" {
			return nil
		}

		dir := filepath.Dir(path)
		if filepath.Base(dir) != componentName || filepath.Base(filepath.Dir(dir)) != "linux" {
			return nil
		}
		context, ok, err := LinuxPackageContextAtDir(dir)
		if err != nil || !ok {
			return err
		}
		matches = append(matches, context)
		return nil
	})
	if err != nil {
		return LinuxPackageContext{}, false, err
	}
	if len(matches) == 0 {
		return LinuxPackageContext{}, false, nil
	}
	if len(matches) > 1 {
		return LinuxPackageContext{}, false, fmt.Errorf("multiple linux package contexts found for component %q", componentName)
	}
	return matches[0], true, nil
}

func resolveCurrentDevopsLinuxDir(findProjectRoot ProjectFinderFunc, dir string, target DockerCommandTarget) (string, bool, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" {
		return "", false, nil
	}

	linuxDir := filepath.Join(dir, "linux")
	if strings.HasSuffix(filepath.Base(dir), "-devops") {
		if ok, err := isLinuxPackageModuleDir(linuxDir); err != nil {
			return "", false, err
		} else if ok {
			return linuxDir, true, nil
		}
	}

	projectRoot, err := resolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil {
		return "", false, err
	}
	if projectRoot == "" || dir != filepath.Clean(projectRoot) {
		return "", false, nil
	}

	return resolveProjectRootDevopsLinuxDir(findProjectRoot, projectRoot)
}

func resolveProjectRootDevopsLinuxDir(findProjectRoot ProjectFinderFunc, projectRoot string) (string, bool, error) {
	projectRoot = filepath.Clean(strings.TrimSpace(projectRoot))
	if projectRoot == "" {
		return "", false, nil
	}

	if tenant, detectedProjectRoot, err := findProjectRoot(); err == nil &&
		filepath.Clean(strings.TrimSpace(detectedProjectRoot)) == projectRoot &&
		strings.TrimSpace(tenant) != "" {
		linuxDir := filepath.Join(projectRoot, RuntimeReleaseName(tenant), "linux")
		if ok, err := isLinuxPackageModuleDir(linuxDir); err != nil {
			return "", false, err
		} else if ok {
			return linuxDir, true, nil
		}
	}

	candidates, err := findDevopsLinuxDirs(projectRoot)
	if err != nil {
		return "", false, err
	}
	switch len(candidates) {
	case 0:
		return "", false, nil
	case 1:
		return candidates[0], true, nil
	default:
		return "", false, fmt.Errorf("multiple devops linux directories found under project root")
	}
}

func findDevopsLinuxDirs(projectRoot string) ([]string, error) {
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		return nil, err
	}
	candidates := make([]string, 0, 1)
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasSuffix(entry.Name(), "-devops") {
			continue
		}
		linuxDir := filepath.Join(projectRoot, entry.Name(), "linux")
		ok, err := isLinuxPackageModuleDir(linuxDir)
		if err != nil {
			return nil, err
		}
		if ok {
			candidates = append(candidates, linuxDir)
		}
	}
	return candidates, nil
}

func isLinuxPackageModuleDir(dir string) (bool, error) {
	contexts, err := ResolveLinuxPackageContextsAtDir(dir)
	if err != nil {
		if errors.Is(err, ErrLinuxPackageBuildNotFound) {
			return false, nil
		}
		return false, err
	}
	return len(contexts) > 0, nil
}

func linuxPackageScriptPath(dir, name string) (string, bool, error) {
	path := filepath.Join(dir, name)
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	if info.IsDir() {
		return "", false, nil
	}
	return path, true, nil
}

func newScriptSpec(dir, path, version string) scriptSpec {
	return scriptSpec{
		Dir:  dir,
		Path: path,
		Env:  buildScriptEnv(version),
	}
}

func resolveDockerBuildRegistryForEnvironment(projectRoot, environment string) (string, error) {
	registry := DefaultContainerRegistry
	if projectRoot == "" {
		return registry, nil
	}

	projectConfig, _, err := LoadProjectConfig(projectRoot)
	if err != nil {
		if errors.Is(err, ErrNotInitialized) {
			return registry, nil
		}
		return "", err
	}

	if configured := projectConfig.ContainerRegistryForEnvironment(environment); configured != "" {
		return configured, nil
	}

	if configured := singleProjectContainerRegistry(projectConfig); configured != "" {
		return configured, nil
	}

	return registry, nil
}

func resolveDockerBuildSkipIfExists(projectRoot, environment string, image DockerImageReference) (bool, error) {
	if strings.TrimSpace(projectRoot) == "" {
		return false, nil
	}

	projectConfig, _, err := LoadProjectConfig(projectRoot)
	if err != nil {
		if errors.Is(err, ErrNotInitialized) {
			return false, nil
		}
		return false, err
	}

	return dockerSkipIfExistsMatches(image, projectConfig.DockerSkipIfExistsForEnvironment(environment)), nil
}

func dockerSkipIfExistsMatches(image DockerImageReference, configured []string) bool {
	if len(configured) == 0 {
		return false
	}

	imageName := normalizeDockerSkipImageName(image.ImageName)
	repository := normalizeDockerSkipImageName(dockerImageRepository(image.Tag))
	for _, candidate := range configured {
		candidate = normalizeDockerSkipImageName(candidate)
		if candidate == "" {
			continue
		}
		if candidate == imageName || candidate == repository {
			return true
		}
	}
	return false
}

func normalizeDockerSkipImageName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return dockerImageRepository(value)
}

func dockerImageRepository(value string) string {
	value = strings.TrimSpace(value)
	lastSlash := strings.LastIndex(value, "/")
	lastColon := strings.LastIndex(value, ":")
	if lastColon > lastSlash {
		return value[:lastColon]
	}
	return value
}

func ResolveDockerBuildContextDirForProject(buildDir, projectRoot string) string {
	if shouldUseProjectRootAsDockerContext(buildDir, projectRoot) {
		return projectRoot
	}
	return buildDir
}

func ResolveDockerBuildVersion(buildDir, projectRoot string) (string, bool, string, error) {
	for _, candidate := range dockerBuildVersionCandidates(buildDir, projectRoot) {
		version, ok, err := loadVersionValue(candidate)
		if err != nil {
			return "", false, "", err
		}
		if ok {
			return version, filepath.Clean(filepath.Dir(candidate)) == filepath.Clean(buildDir), filepath.Clean(candidate), nil
		}
	}

	return "", false, "", ErrVersionFileNotFound
}

func dockerBuildVersionCandidates(buildDir, projectRoot string) []string {
	dirs := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)
	addDir := func(dir string) {
		dir = filepath.Clean(dir)
		if dir == "" {
			return
		}
		if _, ok := seen[dir]; ok {
			return
		}
		seen[dir] = struct{}{}
		dirs = append(dirs, dir)
	}

	addDir(buildDir)

	if filepath.Base(filepath.Dir(buildDir)) == "docker" {
		for dir := filepath.Dir(filepath.Dir(buildDir)); dir != ""; {
			addDir(dir)
			if projectRoot != "" && filepath.Clean(dir) == filepath.Clean(projectRoot) {
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	} else {
		for dir := filepath.Dir(buildDir); dir != ""; {
			addDir(dir)
			if projectRoot != "" && filepath.Clean(dir) == filepath.Clean(projectRoot) {
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	paths := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		paths = append(paths, filepath.Join(dir, "VERSION"))
	}
	return paths
}

func formatLocalSnapshotVersion(version string, now time.Time) string {
	return fmt.Sprintf("%s-snapshot-%s", strings.TrimSpace(version), now.UTC().Format(localSnapshotTimestampFormat))
}

func shouldUseProjectRootAsDockerContext(buildDir, projectRoot string) bool {
	if projectRoot == "" {
		return false
	}

	relative, err := filepath.Rel(projectRoot, buildDir)
	if err != nil {
		return false
	}

	parts := strings.Split(filepath.ToSlash(filepath.Clean(relative)), "/")
	return len(parts) >= 3 && parts[1] == "docker"
}

func IsDockerPushAuthorizationError(message string) bool {
	message = strings.ToLower(message)
	for _, marker := range []string{
		"insufficient_scope",
		"authorization failed",
		"unauthorized",
		"access denied",
		"requested access to the resource is denied",
		"no basic auth credentials",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func dockerRegistryFromImageTag(tag string) string {
	first, _, ok := strings.Cut(tag, "/")
	if !ok {
		return ""
	}
	if strings.Contains(first, ".") || strings.Contains(first, ":") || first == "localhost" {
		return first
	}
	return ""
}

func DockerRegistryDisplayName(registry string) string {
	if strings.TrimSpace(registry) == "" {
		return "Docker Hub"
	}
	return registry
}

func (e DockerRegistryAuthError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "docker registry authorization failed"
}

func (e DockerRegistryAuthError) Unwrap() error {
	return e.Err
}

func isLocalEnvironment(environment string) bool {
	return strings.EqualFold(strings.TrimSpace(environment), DefaultEnvironment)
}

func singleProjectContainerRegistry(projectConfig ProjectConfig) string {
	registry := ""
	for _, envConfig := range projectConfig.Environments {
		current := strings.TrimSpace(envConfig.ContainerRegistry)
		if current == "" {
			continue
		}
		if registry != "" {
			return ""
		}
		registry = current
	}
	return registry
}

func loadVersionValue(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}

	version := strings.TrimSpace(string(data))
	if version == "" {
		return "", false, fmt.Errorf("version file is empty: %s", path)
	}
	return version, true, nil
}
