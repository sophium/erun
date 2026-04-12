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
	"strings"
	"time"
)

const localSnapshotTimestampFormat = "20060102150405"

var ErrVersionFileNotFound = errors.New("version file not found for current module")

type commandSpec struct {
	Dir  string   `json:"dir,omitempty"`
	Name string   `json:"name"`
	Args []string `json:"args"`
}

type (
	BuildContextResolverFunc func() (DockerBuildContext, error)
	NowFunc                  func() time.Time
	DockerImageBuilderFunc   func(string, string, string, io.Writer, io.Writer) error
	DockerImagePusherFunc    func(string, io.Writer, io.Writer) error
	DockerRegistryLoginFunc  func(string, io.Reader, io.Writer, io.Writer) error
	BuildScriptRunnerFunc    func(string, string, io.Reader, io.Writer, io.Writer) error
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
}

type DockerPushSpec struct {
	Dir   string
	Image DockerImageReference
}

type projectBuildScriptSpec struct {
	Dir  string
	Path string
}

type BuildExecutionSpec struct {
	script       *projectBuildScriptSpec
	dockerBuilds []DockerBuildSpec
}

type DockerPushExecutionSpec struct {
	builds []DockerBuildSpec
	pushes []DockerPushSpec
}

type DockerCommandTarget struct {
	ProjectRoot string
	Environment string
}

type DockerRegistryAuthError struct {
	Tag      string
	Registry string
	Message  string
	Err      error
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

	script, err := resolveProjectBuildScript(findProjectRoot, target)
	if err != nil {
		return BuildExecutionSpec{}, err
	}
	if script != nil {
		return BuildExecutionSpec{script: script}, nil
	}

	builds, err := ResolveCurrentDockerBuildSpecs(store, findProjectRoot, resolveBuildContext, now, target)
	if err != nil {
		return BuildExecutionSpec{}, err
	}

	return BuildExecutionSpec{dockerBuilds: builds}, nil
}

func BuildExecutionSpecFromDockerBuilds(builds []DockerBuildSpec) BuildExecutionSpec {
	return BuildExecutionSpec{dockerBuilds: builds}
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
	execution, err := ResolveDockerPushExecution(store, findProjectRoot, resolveBuildContext, now, target)
	if err != nil {
		return DockerPushSpec{}, nil, err
	}
	if len(execution.pushes) != 1 {
		return DockerPushSpec{}, nil, fmt.Errorf("expected exactly one Docker push spec, got %d", len(execution.pushes))
	}
	if len(execution.builds) > 1 {
		return DockerPushSpec{}, nil, fmt.Errorf("expected at most one Docker build spec, got %d", len(execution.builds))
	}

	var build *DockerBuildSpec
	if len(execution.builds) == 1 {
		build = &execution.builds[0]
	}
	return execution.pushes[0], build, nil
}

func ResolveDockerImageReference(store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, buildDir string, target DockerCommandTarget) (DockerImageReference, error) {
	store, findProjectRoot, _, now = normalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

	projectRoot, err := resolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil {
		return DockerImageReference{}, err
	}

	environment, err := resolveDockerBuildEnvironment(store, findProjectRoot, projectRoot, target.Environment)
	if err != nil {
		return DockerImageReference{}, err
	}

	return resolveDockerImageReferenceForProject(now, projectRoot, environment, buildDir)
}

func resolveCurrentDockerBuildSpec(store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, target DockerCommandTarget) (DockerBuildSpec, error) {
	store, findProjectRoot, resolveBuildContext, now = normalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

	buildContext, err := resolveSingleCurrentDockerBuildContext(findProjectRoot, resolveBuildContext, target)
	if err != nil {
		return DockerBuildSpec{}, err
	}

	return resolveDockerBuildSpec(store, findProjectRoot, resolveBuildContext, now, buildContext, target)
}

func ResolveDockerBuildForComponent(store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, projectRoot, environment, componentName string) (*DockerBuildSpec, error) {
	_, _, resolveBuildContext, now = normalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

	if !isLocalEnvironment(environment) {
		return nil, nil
	}

	if resolveBuildContext != nil {
		buildContext, err := resolveBuildContext()
		if err == nil {
			dir := filepath.Clean(strings.TrimSpace(buildContext.Dir))
			if filepath.Base(dir) == strings.TrimSpace(componentName) &&
				filepath.Base(filepath.Dir(dir)) == "docker" &&
				strings.TrimSpace(buildContext.DockerfilePath) != "" {
				build, err := newDockerBuildSpec(now, projectRoot, environment, buildContext)
				if err != nil {
					return nil, err
				}
				return &build, nil
			}
		}
	}

	buildContext, ok, err := FindComponentDockerBuildContext(projectRoot, componentName)
	if err != nil || !ok {
		return nil, err
	}

	build, err := newDockerBuildSpec(now, projectRoot, environment, buildContext)
	if err != nil {
		return nil, err
	}
	return &build, nil
}

func ResolveDockerBuildForImageReference(store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, projectRoot, image string) (DockerBuildSpec, bool, error) {
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

	return DockerBuildSpec{
		ContextDir:     ResolveDockerBuildContextDirForProject(buildContext.Dir, projectRoot),
		DockerfilePath: buildContext.DockerfilePath,
		Image: DockerImageReference{
			ProjectRoot: projectRoot,
			Registry:    registry,
			ImageName:   imageName,
			Version:     version,
			Tag:         image,
		},
	}, true, nil
}

func resolveProjectBuildScript(findProjectRoot ProjectFinderFunc, target DockerCommandTarget) (*projectBuildScriptSpec, error) {
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
		return &projectBuildScriptSpec{
			Dir:  projectRoot,
			Path: "./build.sh",
		}, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	var script *projectBuildScriptSpec
	err = filepath.WalkDir(projectRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || isProjectBuildArtifactDir(path, projectRoot) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "build.sh" {
			return nil
		}

		script = &projectBuildScriptSpec{
			Dir:  filepath.Dir(path),
			Path: "./build.sh",
		}
		return fs.SkipAll
	})
	if err != nil {
		if errors.Is(err, fs.SkipAll) {
			return script, nil
		}
		return nil, err
	}
	return script, nil
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

	return filepath.Base(filepath.Dir(path)) == "docker"
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

func resolveSingleCurrentDockerBuildContext(findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, target DockerCommandTarget) (DockerBuildContext, error) {
	buildContexts, err := ResolveCurrentDockerBuildContexts(findProjectRoot, resolveBuildContext, target)
	if err != nil {
		return DockerBuildContext{}, err
	}
	if len(buildContexts) != 1 {
		return DockerBuildContext{}, fmt.Errorf("expected exactly one Docker build context, got %d", len(buildContexts))
	}
	return buildContexts[0], nil
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

	return nil, fmt.Errorf("dockerfile not found in current directory")
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

	if tenant, detectedProjectRoot, err := findProjectRoot(); err == nil &&
		filepath.Clean(strings.TrimSpace(detectedProjectRoot)) == projectRoot &&
		strings.TrimSpace(tenant) != "" {
		dockerDir := filepath.Join(projectRoot, RuntimeReleaseName(tenant), "docker")
		if ok, err := isDockerBuildModuleDir(dockerDir); err != nil {
			return "", false, err
		} else if ok {
			return dockerDir, true, nil
		}
	}

	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		return "", false, err
	}

	candidates := make([]string, 0, 1)
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasSuffix(entry.Name(), "-devops") {
			continue
		}

		dockerDir := filepath.Join(projectRoot, entry.Name(), "docker")
		ok, err := isDockerBuildModuleDir(dockerDir)
		if err != nil {
			return "", false, err
		}
		if ok {
			candidates = append(candidates, dockerDir)
		}
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

func isDockerBuildModuleDir(dir string) (bool, error) {
	buildContexts, err := ResolveDockerBuildContextsAtDir(dir)
	if err != nil {
		if err.Error() == "dockerfile not found in current directory" {
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

	tenants, err := store.ListTenantConfigs()
	if err != nil {
		if errors.Is(err, ErrNotInitialized) {
			return "", nil
		}
		return "", err
	}

	cleanProjectRoot := filepath.Clean(projectRoot)
	for _, tenantConfig := range tenants {
		if filepath.Clean(tenantConfig.ProjectRoot) != cleanProjectRoot {
			continue
		}
		return strings.TrimSpace(tenantConfig.DefaultEnvironment), nil
	}

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

	return newDockerBuildSpec(now, projectRoot, environment, buildContext)
}

func resolveDockerImageReferenceForProject(now NowFunc, projectRoot, environment, buildDir string) (DockerImageReference, error) {
	registry, err := resolveDockerBuildRegistryForEnvironment(projectRoot, environment)
	if err != nil {
		return DockerImageReference{}, err
	}

	imageName := strings.TrimSpace(filepath.Base(buildDir))
	if imageName == "" || imageName == "." || imageName == string(filepath.Separator) {
		return DockerImageReference{}, fmt.Errorf("could not determine image name from current directory")
	}

	version, versionFromBuildDir, versionFilePath, err := resolveDockerImageVersion(now, projectRoot, environment, buildDir)
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

func resolveDockerImageVersion(now NowFunc, projectRoot, environment, buildDir string) (string, bool, string, error) {
	baseVersion, versionFromBuildDir, versionFilePath, err := ResolveDockerBuildVersion(buildDir, projectRoot)
	if err != nil {
		return "", false, "", err
	}
	if !isLocalEnvironment(environment) || versionFromBuildDir {
		return baseVersion, versionFromBuildDir, versionFilePath, nil
	}
	return formatLocalSnapshotVersion(baseVersion, now()), versionFromBuildDir, versionFilePath, nil
}

func newDockerBuildSpec(now NowFunc, projectRoot, environment string, buildContext DockerBuildContext) (DockerBuildSpec, error) {
	if strings.TrimSpace(buildContext.DockerfilePath) == "" {
		var err error
		buildContext, err = DockerBuildContextAtDir(buildContext.Dir)
		if err != nil {
			return DockerBuildSpec{}, err
		}
	}

	contextDir := ResolveDockerBuildContextDirForProject(buildContext.Dir, projectRoot)
	imageRef, err := resolveDockerImageReferenceForProject(now, projectRoot, environment, buildContext.Dir)
	if err != nil {
		return DockerBuildSpec{}, err
	}

	return DockerBuildSpec{
		ContextDir:     contextDir,
		DockerfilePath: buildContext.DockerfilePath,
		Image:          imageRef,
	}, nil
}

func (b DockerBuildSpec) command() commandSpec {
	return commandSpec{
		Dir:  b.ContextDir,
		Name: "docker",
		Args: []string{"build", "-t", b.Image.Tag, "-f", b.DockerfilePath, "."},
	}
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
	if build == nil {
		build = DockerImageBuilder
	}
	command := buildInput.command()
	ctx.TraceCommand(command.Dir, command.Name, command.Args...)
	if ctx.DryRun {
		return nil
	}
	return build(buildInput.ContextDir, buildInput.DockerfilePath, buildInput.Image.Tag, ctx.Stdout, ctx.Stderr)
}

func RunDockerBuilds(ctx Context, builds []DockerBuildSpec, build DockerImageBuilderFunc) error {
	for _, buildInput := range builds {
		if err := RunDockerBuild(ctx, buildInput, build); err != nil {
			return err
		}
	}
	return nil
}

func RunBuildExecution(ctx Context, execution BuildExecutionSpec, runScript BuildScriptRunnerFunc, build DockerImageBuilderFunc) error {
	if execution.script != nil {
		return runBuildScript(ctx, *execution.script, runScript)
	}
	return RunDockerBuilds(ctx, execution.dockerBuilds, build)
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
		if err := RunDockerBuild(ctx, *buildInput, build); err != nil {
			return err
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
	for _, pushInput := range execution.pushes {
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

func ResolveDockerBuildContextsAtDir(dir string) ([]DockerBuildContext, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || filepath.Base(dir) != "docker" {
		return nil, fmt.Errorf("dockerfile not found in current directory")
	}

	buildContexts, err := DockerBuildContextsUnderDir(dir)
	if err != nil {
		return nil, err
	}
	if len(buildContexts) == 0 {
		return nil, fmt.Errorf("dockerfile not found in current directory")
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

func DockerImageBuilder(dir, dockerfilePath, tag string, stdout, stderr io.Writer) error {
	cmd := exec.Command("docker", "build", "-t", tag, "-f", dockerfilePath, ".")
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func BuildScriptRunner(dir, scriptPath string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := exec.Command(scriptPath)
	cmd.Dir = dir
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func DockerImagePusher(tag string, stdout, stderr io.Writer) error {
	pushCmd := exec.Command("docker", "push", tag)
	output := new(bytes.Buffer)
	pushCmd.Stdout = io.MultiWriter(stdout, output)
	pushCmd.Stderr = io.MultiWriter(stderr, output)
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

func runBuildScript(ctx Context, script projectBuildScriptSpec, run BuildScriptRunnerFunc) error {
	if run == nil {
		run = BuildScriptRunner
	}
	command := commandSpec{
		Dir:  script.Dir,
		Name: script.Path,
	}
	ctx.TraceCommand(command.Dir, command.Name, command.Args...)
	if ctx.DryRun {
		return nil
	}
	return run(script.Dir, script.Path, ctx.Stdin, ctx.Stdout, ctx.Stderr)
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
