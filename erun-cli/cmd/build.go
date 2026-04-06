package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/manifoldco/promptui"
	"github.com/sophium/erun/internal"
	"github.com/sophium/erun/internal/bootstrap"
	"github.com/spf13/cobra"
)

const (
	loginAndRetryPushOption      = "Login and retry push"
	cancelPushOption             = "Cancel"
	localSnapshotTimestampFormat = "20060102150405"
)

var errVersionFileNotFound = errors.New("version file not found for current module")

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

type DockerBuildPlan struct {
	ContextDir     string
	DockerfilePath string
	Image          DockerImageReference
}

type DockerPushPlan struct {
	Dir   string
	Image DockerImageReference
}

type DockerBuildRequest struct {
	Dir            string
	DockerfilePath string
	Tag            string
	Stdout         io.Writer
	Stderr         io.Writer
}

type DockerPushRequest struct {
	Tag    string
	Stdout io.Writer
	Stderr io.Writer
}

type DockerLoginRequest struct {
	Registry string
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer
}

type dockerRegistryAuthError struct {
	tag      string
	registry string
	message  string
	err      error
}

func NewDevopsCmd(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "devops",
		Short:         "DevOps utilities",
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	containerCmd := &cobra.Command{
		Use:           "container",
		Short:         "Container utilities",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	containerCmd.AddCommand(newContainerBuildCmd(deps))
	containerCmd.AddCommand(newContainerPushCmd(deps))
	cmd.AddCommand(containerCmd)
	cmd.AddCommand(newK8sCmd(deps))
	return cmd
}

func NewBuildCmd(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "build",
		Short:         "Build the container image in the current directory",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runContainerBuildCommand(cmd, deps)
		},
	}
	addDryRunFlag(cmd)
	return cmd
}

func NewPushCmd(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "push",
		Short:         "Push the current container image",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runContainerPushCommand(cmd, deps)
		},
	}
	addDryRunFlag(cmd)
	return cmd
}

func newContainerBuildCmd(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "build",
		Short:         "Build the container image in the current directory",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runContainerBuildCommand(cmd, deps)
		},
	}
	addDryRunFlag(cmd)
	return cmd
}

func newContainerPushCmd(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "push",
		Short:         "Push the current container image",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runContainerPushCommand(cmd, deps)
		},
	}
	addDryRunFlag(cmd)
	return cmd
}

func runContainerBuildCommand(cmd *cobra.Command, deps Dependencies) error {
	deps = withDependencyDefaults(deps)

	plans, decisionNotes, err := resolveDockerBuildPlans(deps)
	if err != nil {
		return err
	}

	emitTraceNotes(cmd, cmd.ErrOrStderr(), decisionNotes...)
	for _, plan := range plans {
		emitCommandTrace(cmd, cmd.ErrOrStderr(), plan.Trace())
		emitTraceNotes(cmd, cmd.ErrOrStderr(), plan.DecisionNotes()...)
	}
	if isDryRunCommand(cmd) {
		return nil
	}

	for _, plan := range plans {
		if err := deps.BuildDockerImage(plan.Request(cmd.OutOrStdout(), cmd.ErrOrStderr())); err != nil {
			return err
		}
	}

	return nil
}

func runContainerPushCommand(cmd *cobra.Command, deps Dependencies) error {
	deps = withDependencyDefaults(deps)

	pushPlan, buildPlan, err := resolveDockerPushExecution(deps)
	if err != nil {
		return err
	}

	if buildPlan != nil {
		emitCommandTrace(cmd, cmd.ErrOrStderr(), buildPlan.Trace())
		emitTraceNotes(cmd, cmd.ErrOrStderr(), buildPlan.DecisionNotes()...)
		emitTraceNotes(cmd, cmd.ErrOrStderr(), "decision: building image before push because the local environment push path rebuilds the resolved image tag")
	}
	emitCommandTrace(cmd, cmd.ErrOrStderr(), pushPlan.Trace())
	if buildPlan == nil {
		emitTraceNotes(cmd, cmd.ErrOrStderr(), "decision: pushing the resolved image tag without a local rebuild")
	}
	if isDryRunCommand(cmd) {
		return nil
	}

	if buildPlan != nil {
		if err := deps.BuildDockerImage(buildPlan.Request(cmd.OutOrStdout(), cmd.ErrOrStderr())); err != nil {
			return err
		}
	}

	return executeDockerPushPlan(cmd, deps, pushPlan)
}

func executeDockerPushPlan(cmd *cobra.Command, deps Dependencies, pushPlan DockerPushPlan) error {
	pushReq := pushPlan.Request(cmd.OutOrStdout(), cmd.ErrOrStderr())
	err := deps.PushDockerImage(pushReq)
	if err == nil {
		return nil
	}

	var authErr dockerRegistryAuthError
	if !errors.As(err, &authErr) {
		return err
	}

	retry, promptErr := promptDockerLoginRetry(deps.SelectRunner, authErr.registry)
	if promptErr != nil {
		return promptErr
	}
	if !retry {
		return err
	}

	emitCommandTrace(cmd, cmd.ErrOrStderr(), dockerLoginTrace(pushPlan.Dir, authErr.registry))
	if loginErr := deps.LoginToDockerRegistry(DockerLoginRequest{
		Registry: authErr.registry,
		Stdin:    cmd.InOrStdin(),
		Stdout:   cmd.OutOrStdout(),
		Stderr:   cmd.ErrOrStderr(),
	}); loginErr != nil {
		return loginErr
	}

	return deps.PushDockerImage(pushReq)
}

func (p DockerBuildPlan) Request(stdout, stderr io.Writer) DockerBuildRequest {
	return DockerBuildRequest{
		Dir:            p.ContextDir,
		DockerfilePath: p.DockerfilePath,
		Tag:            p.Image.Tag,
		Stdout:         stdout,
		Stderr:         stderr,
	}
}

func (p DockerBuildPlan) Trace() CommandTrace {
	return CommandTrace{
		Dir:  p.ContextDir,
		Name: "docker",
		Args: []string{"build", "-t", p.Image.Tag, "-f", p.DockerfilePath, "."},
	}
}

func (p DockerBuildPlan) DecisionNotes() []string {
	notes := make([]string, 0, 4)
	if strings.TrimSpace(p.Image.Environment) != "" {
		notes = append(notes, "decision: resolved environment="+p.Image.Environment)
	}
	if strings.TrimSpace(p.Image.Registry) != "" {
		notes = append(notes, "decision: resolved registry="+p.Image.Registry)
	}
	buildDir := filepath.Clean(filepath.Dir(p.DockerfilePath))
	if filepath.Clean(p.ContextDir) != buildDir {
		notes = append(notes, "decision: using project root as Docker build context because the current directory is a docker component")
	}
	if note := dockerImageVersionDecisionNote(p.Image); note != "" {
		notes = append(notes, note)
	}
	return notes
}

func (p DockerPushPlan) Request(stdout, stderr io.Writer) DockerPushRequest {
	return DockerPushRequest{
		Tag:    p.Image.Tag,
		Stdout: stdout,
		Stderr: stderr,
	}
}

func (p DockerPushPlan) Trace() CommandTrace {
	return CommandTrace{
		Dir:  p.Dir,
		Name: "docker",
		Args: []string{"push", p.Image.Tag},
	}
}

func newDockerPushPlan(dir string, image DockerImageReference) DockerPushPlan {
	return DockerPushPlan{
		Dir:   dir,
		Image: image,
	}
}

func dockerLoginTrace(dir, registry string) CommandTrace {
	args := []string{"login"}
	if strings.TrimSpace(registry) != "" {
		args = append(args, registry)
	}
	return CommandTrace{
		Dir:  dir,
		Name: "docker",
		Args: args,
	}
}

func resolveDockerBuildPlan(deps Dependencies) (DockerBuildPlan, error) {
	plans, _, err := resolveDockerBuildPlans(deps)
	if err != nil {
		return DockerBuildPlan{}, err
	}
	if len(plans) != 1 {
		return DockerBuildPlan{}, fmt.Errorf("expected exactly one Docker build plan, got %d", len(plans))
	}
	return plans[0], nil
}

func resolveDockerBuildPlans(deps Dependencies) ([]DockerBuildPlan, []string, error) {
	buildContexts, decisionNotes, err := resolveCurrentDockerBuildContexts(deps)
	if err != nil {
		return nil, nil, err
	}

	projectRoot, err := resolveDockerBuildProjectRoot(deps)
	if err != nil {
		return nil, nil, err
	}

	environment, err := resolveDockerBuildEnvironment(deps, projectRoot)
	if err != nil {
		return nil, nil, err
	}

	plans := make([]DockerBuildPlan, 0, len(buildContexts))
	for _, buildContext := range buildContexts {
		plan, err := newDockerBuildPlan(deps, projectRoot, environment, buildContext)
		if err != nil {
			return nil, nil, err
		}
		plans = append(plans, plan)
	}

	return plans, decisionNotes, nil
}

func newDockerBuildPlan(deps Dependencies, projectRoot, environment string, buildContext DockerBuildContext) (DockerBuildPlan, error) {
	if strings.TrimSpace(buildContext.DockerfilePath) == "" {
		var err error
		buildContext, err = dockerBuildContextAtDir(buildContext.Dir)
		if err != nil {
			return DockerBuildPlan{}, err
		}
	}

	contextDir := resolveDockerBuildContextDirForProject(buildContext.Dir, projectRoot)
	imageRef, err := resolveDockerImageReferenceForProject(deps, projectRoot, environment, buildContext.Dir)
	if err != nil {
		return DockerBuildPlan{}, err
	}

	return DockerBuildPlan{
		ContextDir:     contextDir,
		DockerfilePath: buildContext.DockerfilePath,
		Image:          imageRef,
	}, nil
}

func resolveDockerPushExecution(deps Dependencies) (DockerPushPlan, *DockerBuildPlan, error) {
	buildContext, err := resolveCurrentDockerBuildContext(deps)
	if err != nil {
		return DockerPushPlan{}, nil, err
	}

	imageRef, err := resolveDockerImageReference(deps, buildContext.Dir)
	if err != nil {
		return DockerPushPlan{}, nil, err
	}

	var buildPlan *DockerBuildPlan
	if imageRef.IsLocalBuild {
		plan, err := resolveDockerBuildPlan(deps)
		if err != nil {
			return DockerPushPlan{}, nil, err
		}
		buildPlan = &plan
		imageRef = plan.Image
	}

	return newDockerPushPlan(buildContext.Dir, imageRef), buildPlan, nil
}

func resolveCurrentDockerBuildContext(deps Dependencies) (DockerBuildContext, error) {
	buildContexts, _, err := resolveCurrentDockerBuildContexts(deps)
	if err != nil {
		return DockerBuildContext{}, err
	}
	if len(buildContexts) != 1 {
		return DockerBuildContext{}, fmt.Errorf("expected exactly one Docker build context, got %d", len(buildContexts))
	}
	return buildContexts[0], nil
}

func resolveCurrentDockerBuildContexts(deps Dependencies) ([]DockerBuildContext, []string, error) {
	buildContext, err := deps.ResolveDockerBuildContext()
	if err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(buildContext.DockerfilePath) != "" {
		return []DockerBuildContext{buildContext}, nil, nil
	}

	return resolveDockerBuildContextsAtDir(buildContext.Dir)
}

func defaultDockerBuildContextResolver() (DockerBuildContext, error) {
	dir, err := os.Getwd()
	if err != nil {
		return DockerBuildContext{}, err
	}

	return dockerBuildContextAtDir(dir)
}

func dockerBuildContextAtDir(dir string) (DockerBuildContext, error) {
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

	return DockerBuildContext{
		Dir:            dir,
		DockerfilePath: dockerfilePath,
	}, nil
}

func resolveDockerBuildContextsAtDir(dir string) ([]DockerBuildContext, []string, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || filepath.Base(dir) != "docker" {
		return nil, nil, fmt.Errorf("dockerfile not found in current directory")
	}

	buildContexts, err := dockerBuildContextsUnderDir(dir)
	if err != nil {
		return nil, nil, err
	}
	if len(buildContexts) == 0 {
		return nil, nil, fmt.Errorf("dockerfile not found in current directory")
	}

	return buildContexts, []string{"decision: building all Docker component images because the current directory is a docker module directory"}, nil
}

func dockerBuildContextsUnderDir(dir string) ([]DockerBuildContext, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	buildContexts := make([]DockerBuildContext, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		buildContext, err := dockerBuildContextAtDir(filepath.Join(dir, entry.Name()))
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

func resolveDockerBuildPlanForComponent(deps Dependencies, projectRoot, environment, componentName string) (*DockerBuildPlan, error) {
	if !isLocalEnvironment(environment) {
		return nil, nil
	}

	buildContext, ok, err := findComponentDockerBuildContext(projectRoot, componentName)
	if err != nil || !ok {
		return nil, err
	}

	plan, err := newDockerBuildPlan(deps, projectRoot, environment, buildContext)
	if err != nil {
		return nil, err
	}

	return &plan, nil
}

func findComponentDockerBuildContext(projectRoot, componentName string) (DockerBuildContext, bool, error) {
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

func defaultDockerImageBuilder(req DockerBuildRequest) error {
	cmd := exec.Command("docker", "build", "-t", req.Tag, "-f", req.DockerfilePath, ".")
	cmd.Dir = req.Dir
	cmd.Stdout = req.Stdout
	cmd.Stderr = req.Stderr
	return cmd.Run()
}

func defaultDockerImagePusher(req DockerPushRequest) error {
	pushCmd := exec.Command("docker", "push", req.Tag)
	output := new(bytes.Buffer)
	pushCmd.Stdout = io.MultiWriter(req.Stdout, output)
	pushCmd.Stderr = io.MultiWriter(req.Stderr, output)
	err := pushCmd.Run()
	if err == nil {
		return nil
	}

	message := output.String()
	if isDockerPushAuthorizationError(message) {
		return dockerRegistryAuthError{
			tag:      req.Tag,
			registry: dockerRegistryFromImageTag(req.Tag),
			message:  strings.TrimSpace(message),
			err:      err,
		}
	}

	return err
}

func defaultDockerRegistryLogin(req DockerLoginRequest) error {
	args := []string{"login"}
	if req.Registry != "" {
		args = append(args, req.Registry)
	}

	loginCmd := exec.Command("docker", args...)
	loginCmd.Stdin = req.Stdin
	loginCmd.Stdout = req.Stdout
	loginCmd.Stderr = req.Stderr
	return loginCmd.Run()
}

func resolveDockerBuildTag(deps Dependencies, buildDir string) (string, error) {
	imageRef, err := resolveDockerImageReference(deps, buildDir)
	if err != nil {
		return "", err
	}
	return imageRef.Tag, nil
}

func resolveDockerImageReference(deps Dependencies, buildDir string) (DockerImageReference, error) {
	projectRoot, err := resolveDockerBuildProjectRoot(deps)
	if err != nil {
		return DockerImageReference{}, err
	}

	environment, err := resolveDockerBuildEnvironment(deps, projectRoot)
	if err != nil {
		return DockerImageReference{}, err
	}

	return resolveDockerImageReferenceForProject(deps, projectRoot, environment, buildDir)
}

func resolveDockerImageReferenceForProject(deps Dependencies, projectRoot, environment, buildDir string) (DockerImageReference, error) {
	registry, err := resolveDockerBuildRegistryForEnvironment(projectRoot, environment)
	if err != nil {
		return DockerImageReference{}, err
	}

	imageName := strings.TrimSpace(filepath.Base(buildDir))
	if imageName == "" || imageName == "." || imageName == string(filepath.Separator) {
		return DockerImageReference{}, fmt.Errorf("could not determine image name from current directory")
	}

	version, versionFromBuildDir, versionFilePath, err := resolveDockerImageVersion(deps, projectRoot, environment, buildDir)
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

func resolveDockerImageVersion(deps Dependencies, projectRoot, environment, buildDir string) (string, bool, string, error) {
	baseVersion, versionFromBuildDir, versionFilePath, err := resolveDockerBuildVersion(buildDir, projectRoot)
	if err != nil {
		return "", false, "", err
	}
	if !isLocalEnvironment(environment) || versionFromBuildDir {
		return baseVersion, versionFromBuildDir, versionFilePath, nil
	}

	return formatLocalSnapshotVersion(baseVersion, currentTime(deps)), versionFromBuildDir, versionFilePath, nil
}

func resolveDockerBuildProjectRoot(deps Dependencies) (string, error) {
	finder := deps.FindProjectRoot
	if finder == nil {
		finder = internal.FindProjectRoot
	}

	_, projectRoot, err := finder()
	if err != nil {
		if errors.Is(err, internal.ErrNotInGitRepository) {
			return "", nil
		}
		return "", err
	}
	return projectRoot, nil
}

func resolveDockerBuildContextDir(deps Dependencies, buildDir string) (string, error) {
	projectRoot, err := resolveDockerBuildProjectRoot(deps)
	if err != nil {
		return "", err
	}
	return resolveDockerBuildContextDirForProject(buildDir, projectRoot), nil
}

func resolveDockerBuildContextDirForProject(buildDir, projectRoot string) string {
	if shouldUseProjectRootAsDockerContext(buildDir, projectRoot) {
		return projectRoot
	}
	return buildDir
}

func resolveDockerBuildRegistryForEnvironment(projectRoot, environment string) (string, error) {
	registry := bootstrap.DefaultContainerRegistry
	if projectRoot == "" {
		return registry, nil
	}

	projectConfig, _, err := internal.LoadProjectConfig(projectRoot)
	if err != nil {
		if errors.Is(err, internal.ErrNotInitialized) {
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

func resolveDockerBuildEnvironment(deps Dependencies, projectRoot string) (string, error) {
	store := deps.Store
	if store == nil {
		store = bootstrap.ConfigStore{}
	}

	tenants, err := store.ListTenantConfigs()
	if err != nil {
		if errors.Is(err, internal.ErrNotInitialized) {
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

	finder := deps.FindProjectRoot
	if finder == nil {
		finder = internal.FindProjectRoot
	}

	tenant, detectedProjectRoot, err := finder()
	if err != nil {
		if errors.Is(err, internal.ErrNotInGitRepository) {
			return "", nil
		}
		return "", err
	}
	if filepath.Clean(detectedProjectRoot) != cleanProjectRoot || strings.TrimSpace(tenant) == "" {
		return "", nil
	}

	tenantConfig, _, err := store.LoadTenantConfig(tenant)
	if err != nil {
		if errors.Is(err, internal.ErrNotInitialized) {
			return "", nil
		}
		return "", err
	}
	if projectRoot := strings.TrimSpace(tenantConfig.ProjectRoot); projectRoot != "" && filepath.Clean(projectRoot) != cleanProjectRoot {
		return "", nil
	}

	return strings.TrimSpace(tenantConfig.DefaultEnvironment), nil
}

func currentTime(deps Dependencies) time.Time {
	if deps.Now != nil {
		return deps.Now()
	}
	return time.Now()
}

func isLocalEnvironment(environment string) bool {
	return strings.EqualFold(strings.TrimSpace(environment), bootstrap.DefaultEnvironment)
}

func formatLocalSnapshotVersion(version string, now time.Time) string {
	return fmt.Sprintf("%s-snapshot-%s", strings.TrimSpace(version), now.UTC().Format(localSnapshotTimestampFormat))
}

func singleProjectContainerRegistry(projectConfig internal.ProjectConfig) string {
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

func resolveDockerBuildVersion(buildDir, projectRoot string) (string, bool, string, error) {
	for _, candidate := range dockerBuildVersionCandidates(buildDir, projectRoot) {
		version, ok, err := loadVersionValue(candidate)
		if err != nil {
			return "", false, "", err
		}
		if ok {
			return version, filepath.Clean(filepath.Dir(candidate)) == filepath.Clean(buildDir), filepath.Clean(candidate), nil
		}
	}

	return "", false, "", errVersionFileNotFound
}

func dockerImageVersionDecisionNote(image DockerImageReference) string {
	if strings.TrimSpace(image.VersionFilePath) == "" {
		return ""
	}
	if image.IsLocalBuild && !image.VersionFromBuildDir {
		return fmt.Sprintf("decision: resolved version=%s from inherited VERSION %s and appended the local snapshot suffix", image.Version, image.VersionFilePath)
	}
	if image.VersionFromBuildDir {
		return fmt.Sprintf("decision: resolved version=%s from current build directory VERSION %s", image.Version, image.VersionFilePath)
	}
	return fmt.Sprintf("decision: resolved version=%s from VERSION %s", image.Version, image.VersionFilePath)
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

func promptDockerLoginRetry(run SelectRunner, registry string) (bool, error) {
	label := fmt.Sprintf("Docker push requires login to %s", dockerRegistryDisplayName(registry))
	prompt := promptui.Select{
		Label: label,
		Items: []string{loginAndRetryPushOption, cancelPushOption},
	}

	_, result, err := run(prompt)
	if err != nil {
		if errors.Is(err, promptui.ErrInterrupt) {
			return false, fmt.Errorf("docker login selection interrupted")
		}
		if errors.Is(err, promptui.ErrAbort) {
			return false, nil
		}
		return false, err
	}

	return result == loginAndRetryPushOption, nil
}

func isDockerPushAuthorizationError(message string) bool {
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

func dockerRegistryDisplayName(registry string) string {
	if strings.TrimSpace(registry) == "" {
		return "Docker Hub"
	}
	return registry
}

func (e dockerRegistryAuthError) Error() string {
	if e.message != "" {
		return e.message
	}
	if e.err != nil {
		return e.err.Error()
	}
	return "docker registry authorization failed"
}

func (e dockerRegistryAuthError) Unwrap() error {
	return e.err
}
