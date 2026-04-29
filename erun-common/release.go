package eruncommon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	DefaultReleaseMainBranch    = "main"
	DefaultReleaseDevelopBranch = "develop"
	defaultReleaseGitUserName   = "ERun"
	defaultReleaseGitUserEmail  = "erun@local"
)

type (
	GitValueResolverFunc func(string) (string, error)
	GitCommandRunnerFunc func(string, io.Writer, io.Writer, ...string) error
	GitBranchCheckerFunc func(string, string) (bool, error)
)

type ReleaseMode string

const (
	ReleaseModeStable     ReleaseMode = "stable"
	ReleaseModeCandidate  ReleaseMode = "candidate"
	ReleaseModePrerelease ReleaseMode = "prerelease"
)

type ReleaseParams struct {
	ProjectRoot string
	Force       bool
}

type ReleaseFileUpdate struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type ReleaseChartSpec struct {
	ChartPath  string `json:"chartPath"`
	Version    string `json:"version"`
	AppVersion string `json:"appVersion"`
}

type ReleaseDockerImageSpec struct {
	ContextDir     string `json:"contextDir"`
	DockerfilePath string `json:"dockerfilePath"`
	ImageName      string `json:"imageName"`
	Registry       string `json:"registry,omitempty"`
	Tag            string `json:"tag"`
	Version        string `json:"version"`
}

type ReleaseCommandSpec struct {
	Dir  string   `json:"dir,omitempty"`
	Name string   `json:"name"`
	Args []string `json:"args"`
}

type ReleaseStage struct {
	Name          string                    `json:"name"`
	FileUpdates   []ReleaseFileUpdate       `json:"fileUpdates,omitempty"`
	GitCommands   []ReleaseCommandSpec      `json:"gitCommands,omitempty"`
	PackagingSync *ReleasePackagingSyncSpec `json:"packagingSync,omitempty"`
}

type ReleasePackagingSyncSpec struct {
	Version     string `json:"version"`
	FormulaPath string `json:"formulaPath,omitempty"`
	ScoopPath   string `json:"scoopPath,omitempty"`
}

type ReleaseSpec struct {
	ProjectRoot     string                   `json:"projectRoot"`
	ReleaseRoot     string                   `json:"releaseRoot"`
	Branch          string                   `json:"branch"`
	Commit          string                   `json:"commit"`
	BaseVersion     string                   `json:"baseVersion"`
	Version         string                   `json:"version"`
	NextVersion     string                   `json:"nextVersion,omitempty"`
	VersionFilePath string                   `json:"versionFilePath"`
	Mode            ReleaseMode              `json:"mode"`
	Force           bool                     `json:"force,omitempty"`
	Charts          []ReleaseChartSpec       `json:"charts,omitempty"`
	DockerImages    []ReleaseDockerImageSpec `json:"dockerImages,omitempty"`
	Stages          []ReleaseStage           `json:"stages,omitempty"`
	LinuxReleases   []scriptSpec
	SkippedLinux    bool `json:"-"`
}

type releaseInputs struct {
	ProjectRoot         string
	ReleaseRoot         string
	ReleaseConfig       ReleaseConfig
	Branch              string
	Commit              string
	BaseVersion         string
	Version             string
	VersionFilePath     string
	Mode                ReleaseMode
	DevelopBranchExists bool
}

type releaseArtifacts struct {
	Charts        []ReleaseChartSpec
	Images        []ReleaseDockerImageSpec
	FileUpdates   []ReleaseFileUpdate
	PackagingSync *ReleasePackagingSyncSpec
	LinuxReleases []scriptSpec
	SkippedLinux  bool
}

func ResolveReleaseSpec(findProjectRoot ProjectFinderFunc, params ReleaseParams) (ReleaseSpec, error) {
	return resolveReleaseSpec(findProjectRoot, LoadProjectConfig, GitCurrentBranch, GitShortCommit, GitLocalBranchExists, params)
}

func resolveReleaseSpec(findProjectRoot ProjectFinderFunc, loadProjectConfig ProjectConfigLoaderFunc, resolveBranch, resolveCommit GitValueResolverFunc, branchExists GitBranchCheckerFunc, params ReleaseParams) (ReleaseSpec, error) {
	findProjectRoot, loadProjectConfig, resolveBranch, resolveCommit, branchExists = normalizeReleaseDependencies(findProjectRoot, loadProjectConfig, resolveBranch, resolveCommit, branchExists)

	inputs, err := resolveReleaseInputs(findProjectRoot, loadProjectConfig, resolveBranch, resolveCommit, branchExists, params)
	if err != nil {
		return ReleaseSpec{}, err
	}
	artifacts, err := discoverReleaseArtifacts(inputs)
	if err != nil {
		return ReleaseSpec{}, err
	}
	nextVersion, stages, err := resolveReleaseStages(inputs, artifacts.FileUpdates, artifacts.PackagingSync)
	if err != nil {
		return ReleaseSpec{}, err
	}

	return ReleaseSpec{
		ProjectRoot:     inputs.ProjectRoot,
		ReleaseRoot:     inputs.ReleaseRoot,
		Branch:          inputs.Branch,
		Commit:          inputs.Commit,
		BaseVersion:     inputs.BaseVersion,
		Version:         inputs.Version,
		NextVersion:     nextVersion,
		VersionFilePath: inputs.VersionFilePath,
		Mode:            inputs.Mode,
		Force:           params.Force,
		Charts:          artifacts.Charts,
		DockerImages:    artifacts.Images,
		Stages:          stages,
		LinuxReleases:   artifacts.LinuxReleases,
		SkippedLinux:    artifacts.SkippedLinux,
	}, nil
}

type ReleasePackagingSyncerFunc func(Context, ReleasePackagingSyncSpec) ([]ReleaseFileUpdate, error)

func RunReleaseSpec(ctx Context, spec ReleaseSpec, runGit GitCommandRunnerFunc, runScript BuildScriptRunnerFunc) error {
	return runReleaseSpec(ctx, spec, runGit, runScript, syncReleasePackagingChecksums)
}

func runReleaseSpec(ctx Context, spec ReleaseSpec, runGit GitCommandRunnerFunc, runScript BuildScriptRunnerFunc, syncPackagingChecksums ReleasePackagingSyncerFunc) error {
	if runGit == nil {
		runGit = GitCommandRunner
	}
	if syncPackagingChecksums == nil {
		syncPackagingChecksums = syncReleasePackagingChecksums
	}

	traceReleaseSpec(ctx, spec)
	if err := ensureReleaseWorktreeClean(ctx, spec.ProjectRoot); err != nil {
		return err
	}

	for _, stage := range spec.Stages {
		if err := runReleaseStage(ctx, spec, stage, runGit, syncPackagingChecksums); err != nil {
			return err
		}
	}
	if spec.SkippedLinux {
		ctx.Trace("skipping linux package scripts: host is not Linux or dpkg-deb is unavailable")
	}

	return runScriptSpecs(ctx, spec.LinuxReleases, runScript)
}

func traceReleaseSpec(ctx Context, spec ReleaseSpec) {
	ctx.Trace(fmt.Sprintf("release: branch=%s mode=%s version=%s", spec.Branch, spec.Mode, spec.Version))
	if spec.NextVersion != "" {
		ctx.Trace("next version: " + spec.NextVersion)
	}
	for _, image := range spec.DockerImages {
		ctx.Trace("docker image: " + image.Tag)
	}
}

func ensureReleaseWorktreeClean(ctx Context, projectRoot string) error {
	if ctx.DryRun {
		return nil
	}
	clean, err := gitWorktreeClean(projectRoot)
	if err != nil {
		return err
	}
	if !clean {
		return fmt.Errorf("release requires a clean git worktree; commit or stash changes first")
	}
	return nil
}

func runReleaseStage(ctx Context, spec ReleaseSpec, stage ReleaseStage, runGit GitCommandRunnerFunc, syncPackagingChecksums ReleasePackagingSyncerFunc) error {
	ctx.Trace("stage: " + stage.Name)
	stageFileUpdates, err := releaseStageFileUpdates(ctx, stage, syncPackagingChecksums)
	if err != nil {
		return err
	}
	if err := writeReleaseFileUpdates(ctx, stageFileUpdates); err != nil {
		return err
	}
	for _, command := range releaseStageCommands(ctx, stage, stageFileUpdates) {
		if err := runReleaseCommand(ctx, spec, command, runGit); err != nil {
			return err
		}
	}
	return nil
}

func releaseStageFileUpdates(ctx Context, stage ReleaseStage, syncPackagingChecksums ReleasePackagingSyncerFunc) ([]ReleaseFileUpdate, error) {
	updates := append([]ReleaseFileUpdate{}, stage.FileUpdates...)
	if stage.PackagingSync == nil {
		return updates, nil
	}
	generatedUpdates, err := syncPackagingChecksums(ctx, *stage.PackagingSync)
	if err != nil {
		return nil, err
	}
	return append(updates, generatedUpdates...), nil
}

func writeReleaseFileUpdates(ctx Context, updates []ReleaseFileUpdate) error {
	for _, update := range updates {
		ctx.TraceBlock("write "+update.Path, strings.TrimRight(update.Content, "\n"))
		if ctx.DryRun {
			continue
		}
		if err := os.WriteFile(update.Path, []byte(update.Content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func releaseStageCommands(ctx Context, stage ReleaseStage, updates []ReleaseFileUpdate) []ReleaseCommandSpec {
	if !ctx.DryRun && stage.PackagingSync != nil && len(updates) == 0 {
		return nil
	}
	return stage.GitCommands
}

func runReleaseCommand(ctx Context, spec ReleaseSpec, command ReleaseCommandSpec, runGit GitCommandRunnerFunc) error {
	if command.Name == "git" && shouldSkipExistingReleaseTag(command.Args) {
		skip, err := prepareReleaseTag(ctx, spec, runGit, command)
		if err != nil {
			return err
		}
		if skip {
			ctx.Trace("release tag already exists at HEAD; skipping " + command.Args[2])
			return nil
		}
	}
	ctx.TraceCommand(command.Dir, command.Name, command.Args...)
	if ctx.DryRun {
		return nil
	}
	if command.Name != "git" {
		return fmt.Errorf("unsupported release command %q", command.Name)
	}
	return runGit(command.Dir, ctx.Stdout, ctx.Stderr, command.Args...)
}

func shouldSkipExistingReleaseTag(args []string) bool {
	return len(args) >= 3 && args[0] == "tag" && args[1] == "-a" && strings.TrimSpace(args[2]) != ""
}

func prepareReleaseTag(ctx Context, spec ReleaseSpec, runGit GitCommandRunnerFunc, command ReleaseCommandSpec) (bool, error) {
	tag := strings.TrimSpace(command.Args[2])
	if tag == "" {
		return false, nil
	}
	if spec.Force {
		if err := deleteExistingReleaseTag(ctx, command.Dir, tag, runGit); err != nil {
			return false, err
		}
		return false, nil
	}
	return canSkipExistingReleaseTag(command.Dir, tag)
}

func canSkipExistingReleaseTag(projectRoot, tag string) (bool, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return false, nil
	}

	tagCommit, ok, err := gitResolvedRef(projectRoot, tag+"^{}")
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}

	headCommit, ok, err := gitResolvedRef(projectRoot, "HEAD")
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("could not resolve HEAD for release tag check")
	}
	if tagCommit != headCommit {
		return false, fmt.Errorf("release tag %q already exists at %s, expected current HEAD %s", tag, tagCommit, headCommit)
	}

	return true, nil
}

func deleteExistingReleaseTag(ctx Context, projectRoot, tag string, runGit GitCommandRunnerFunc) error {
	localExists, err := gitTagExists(projectRoot, tag)
	if err != nil {
		return err
	}
	if localExists {
		ctx.TraceCommand(projectRoot, "git", "tag", "-d", tag)
		if !ctx.DryRun {
			if err := runGit(projectRoot, ctx.Stdout, ctx.Stderr, "tag", "-d", tag); err != nil {
				return err
			}
		}
	}

	remoteExists, err := gitRemoteTagExists(projectRoot, "origin", tag)
	if err != nil {
		return err
	}
	if remoteExists {
		ctx.TraceCommand(projectRoot, "git", "push", "--delete", "origin", tag)
		if !ctx.DryRun {
			if err := runGit(projectRoot, ctx.Stdout, ctx.Stderr, "push", "--delete", "origin", tag); err != nil {
				return err
			}
		}
	}

	return nil
}

func gitTagExists(projectRoot, tag string) (bool, error) {
	_, ok, err := gitResolvedRef(projectRoot, tag+"^{}")
	return ok, err
}

func gitRemoteTagExists(projectRoot, remote, tag string) (bool, error) {
	remote = strings.TrimSpace(remote)
	tag = strings.TrimSpace(tag)
	if remote == "" || tag == "" {
		return false, nil
	}

	output, err := exec.Command("git", "-C", projectRoot, "ls-remote", "--tags", "--refs", remote, "refs/tags/"+tag).CombinedOutput()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(output)) != "", nil
}

func gitResolvedRef(projectRoot, ref string) (string, bool, error) {
	output, err := exec.Command("git", "-C", projectRoot, "rev-parse", ref).CombinedOutput()
	if err == nil {
		return strings.TrimSpace(string(output)), true, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() != 0 {
		return "", false, nil
	}
	return "", false, err
}

func GitCurrentBranch(projectRoot string) (string, error) {
	output, err := exec.Command("git", "-C", projectRoot, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func GitShortCommit(projectRoot string) (string, error) {
	output, err := exec.Command("git", "-C", projectRoot, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func GitLocalBranchExists(projectRoot, branch string) (bool, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return false, nil
	}

	cmd := exec.Command("git", "-C", projectRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func GitCommandRunner(dir string, stdout, stderr io.Writer, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = gitCommandEnv(dir)
	return cmd.Run()
}

func gitWorktreeClean(projectRoot string) (bool, error) {
	output, err := exec.Command("git", "-C", projectRoot, "status", "--porcelain").CombinedOutput()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(output)) == "", nil
}

func gitCommandEnv(dir string) []string {
	env := os.Environ()

	authorName := firstNonEmptyEnv(env, "GIT_AUTHOR_NAME")
	authorEmail := firstNonEmptyEnv(env, "GIT_AUTHOR_EMAIL")
	committerName := firstNonEmptyEnv(env, "GIT_COMMITTER_NAME")
	committerEmail := firstNonEmptyEnv(env, "GIT_COMMITTER_EMAIL")

	name := authorName
	if name == "" {
		name = committerName
	}
	if name == "" {
		name = gitConfigValue(dir, "user.name")
	}
	if name == "" {
		name = firstNonEmptyEnv(env, "ERUN_GIT_USER_NAME")
	}
	if name == "" {
		name = defaultReleaseGitUserName
	}

	email := authorEmail
	if email == "" {
		email = committerEmail
	}
	if email == "" {
		email = gitConfigValue(dir, "user.email")
	}
	if email == "" {
		email = firstNonEmptyEnv(env, "ERUN_GIT_USER_EMAIL")
	}
	if email == "" {
		email = defaultReleaseGitUserEmail
	}

	env = appendOrReplaceEnv(env, "GIT_AUTHOR_NAME", name)
	env = appendOrReplaceEnv(env, "GIT_AUTHOR_EMAIL", email)
	env = appendOrReplaceEnv(env, "GIT_COMMITTER_NAME", name)
	env = appendOrReplaceEnv(env, "GIT_COMMITTER_EMAIL", email)

	return env
}

func gitConfigValue(dir, key string) string {
	cmd := exec.Command("git", "config", "--get", key)
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func firstNonEmptyEnv(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(entry, prefix))
		if value != "" {
			return value
		}
	}
	return ""
}

func appendOrReplaceEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			continue
		}
		env[i] = prefix + value
		return env
	}
	return append(env, prefix+value)
}

func normalizeReleaseDependencies(findProjectRoot ProjectFinderFunc, loadProjectConfig ProjectConfigLoaderFunc, resolveBranch, resolveCommit GitValueResolverFunc, branchExists GitBranchCheckerFunc) (ProjectFinderFunc, ProjectConfigLoaderFunc, GitValueResolverFunc, GitValueResolverFunc, GitBranchCheckerFunc) {
	if findProjectRoot == nil {
		findProjectRoot = FindProjectRoot
	}
	if loadProjectConfig == nil {
		loadProjectConfig = LoadProjectConfig
	}
	if resolveBranch == nil {
		resolveBranch = GitCurrentBranch
	}
	if resolveCommit == nil {
		resolveCommit = GitShortCommit
	}
	if branchExists == nil {
		branchExists = GitLocalBranchExists
	}
	return findProjectRoot, loadProjectConfig, resolveBranch, resolveCommit, branchExists
}

func resolveReleaseInputs(findProjectRoot ProjectFinderFunc, loadProjectConfig ProjectConfigLoaderFunc, resolveBranch, resolveCommit GitValueResolverFunc, branchExists GitBranchCheckerFunc, params ReleaseParams) (releaseInputs, error) {
	projectRoot, err := resolveReleaseProjectRoot(findProjectRoot, params)
	if err != nil {
		return releaseInputs{}, err
	}
	releaseRoot, err := resolveReleaseModuleRoot(projectRoot)
	if err != nil {
		return releaseInputs{}, err
	}
	releaseConfig, err := loadReleaseConfig(projectRoot, loadProjectConfig)
	if err != nil {
		return releaseInputs{}, err
	}
	branch, commit, err := resolveReleaseGitState(projectRoot, resolveBranch, resolveCommit)
	if err != nil {
		return releaseInputs{}, err
	}
	baseVersion, _, versionFilePath, err := ResolveDockerBuildVersion(releaseRoot, releaseRoot)
	if err != nil {
		return releaseInputs{}, err
	}
	mode := classifyReleaseMode(branch, releaseConfig)
	developBranchExists, err := branchExists(projectRoot, releaseConfig.DevelopBranch)
	if err != nil {
		return releaseInputs{}, err
	}
	return releaseInputs{
		ProjectRoot:         projectRoot,
		ReleaseRoot:         releaseRoot,
		ReleaseConfig:       releaseConfig,
		Branch:              branch,
		Commit:              commit,
		BaseVersion:         baseVersion,
		Version:             resolveReleaseVersion(baseVersion, commit, mode),
		VersionFilePath:     versionFilePath,
		Mode:                mode,
		DevelopBranchExists: developBranchExists,
	}, nil
}

func loadReleaseConfig(projectRoot string, loadProjectConfig ProjectConfigLoaderFunc) (ReleaseConfig, error) {
	projectConfig, _, err := loadProjectConfig(projectRoot)
	if err != nil && !errors.Is(err, ErrNotInitialized) {
		return ReleaseConfig{}, err
	}
	return projectConfig.NormalizedReleaseConfig(), nil
}

func resolveReleaseGitState(projectRoot string, resolveBranch, resolveCommit GitValueResolverFunc) (string, string, error) {
	branch, err := resolveBranch(projectRoot)
	if err != nil {
		return "", "", err
	}
	commit, err := resolveCommit(projectRoot)
	if err != nil {
		return "", "", err
	}
	return branch, commit, nil
}

func discoverReleaseArtifacts(inputs releaseInputs) (releaseArtifacts, error) {
	charts, fileUpdates, err := discoverReleaseCharts(inputs.ReleaseRoot, inputs.Version)
	if err != nil {
		return releaseArtifacts{}, err
	}
	artifacts := releaseArtifacts{Charts: charts, FileUpdates: fileUpdates}
	if err := discoverStableReleaseArtifacts(inputs, &artifacts); err != nil {
		return releaseArtifacts{}, err
	}
	images, err := discoverReleaseDockerImages(inputs.ProjectRoot, inputs.ReleaseRoot, inputs.VersionFilePath, inputs.Version)
	if err != nil {
		return releaseArtifacts{}, err
	}
	artifacts.Images = images
	linuxReleases, skippedLinux, err := discoverSupportedReleaseLinuxScripts(inputs.ReleaseRoot, inputs.Version)
	if err != nil {
		return releaseArtifacts{}, err
	}
	artifacts.LinuxReleases = linuxReleases
	artifacts.SkippedLinux = skippedLinux
	return artifacts, nil
}

func discoverStableReleaseArtifacts(inputs releaseInputs, artifacts *releaseArtifacts) error {
	if inputs.Mode != ReleaseModeStable {
		return nil
	}
	packagingUpdates, syncSpec, err := discoverStableReleasePackaging(inputs.ProjectRoot, inputs.Version)
	if err != nil {
		return err
	}
	artifacts.FileUpdates = append(artifacts.FileUpdates, packagingUpdates...)
	artifacts.PackagingSync = syncSpec
	return nil
}

func discoverSupportedReleaseLinuxScripts(releaseRoot, version string) ([]scriptSpec, bool, error) {
	linuxReleases, err := discoverReleaseLinuxScripts(releaseRoot, version)
	if err != nil {
		return nil, false, err
	}
	if len(linuxReleases) > 0 && !LinuxPackageBuildsSupported() {
		return nil, true, nil
	}
	return linuxReleases, false, nil
}

func resolveReleaseStages(inputs releaseInputs, releaseFileUpdates []ReleaseFileUpdate, packagingSync *ReleasePackagingSyncSpec) (string, []ReleaseStage, error) {
	stages := baseReleaseStages(inputs, releaseFileUpdates)
	stages = append(stages, stablePackagingStages(inputs, packagingSync)...)
	nextVersion, stableStages, err := stablePostReleaseStages(inputs)
	if err != nil {
		return "", nil, err
	}
	stages = append(stages, stableStages...)
	stages = append(stages, candidatePostReleaseStages(inputs)...)
	return nextVersion, stages, nil
}

func baseReleaseStages(inputs releaseInputs, releaseFileUpdates []ReleaseFileUpdate) []ReleaseStage {
	stages := make([]ReleaseStage, 0, 2)
	stages = appendReleaseStageIfActive(stages, newSyncRemoteStage(inputs.ProjectRoot, inputs.Branch))
	stages = appendReleaseStageIfActive(stages, newReleaseStage(inputs.ProjectRoot, releaseFileUpdates, inputs.Version, inputs.Mode))
	return stages
}

func stablePackagingStages(inputs releaseInputs, packagingSync *ReleasePackagingSyncSpec) []ReleaseStage {
	if inputs.Mode != ReleaseModeStable || packagingSync == nil {
		return nil
	}
	stages := make([]ReleaseStage, 0, 2)
	stages = appendReleaseStageIfActive(stages, newPushReleaseTagStage(inputs.ProjectRoot, inputs.Version))
	stages = appendReleaseStageIfActive(stages, newSyncPackagingStage(inputs.ProjectRoot, *packagingSync))
	return stages
}

func stablePostReleaseStages(inputs releaseInputs) (string, []ReleaseStage, error) {
	if inputs.Mode != ReleaseModeStable {
		return "", nil, nil
	}
	nextVersion, err := nextPatchVersion(inputs.BaseVersion)
	if err != nil {
		return "", nil, err
	}
	stages := make([]ReleaseStage, 0, 3)
	stages = appendReleaseStageIfActive(stages, newStableBumpStage(inputs, nextVersion))
	stages = appendReleaseStageIfActive(stages, newSyncDevelopStage(inputs.ProjectRoot, inputs.ReleaseConfig, inputs.DevelopBranchExists))
	stages = appendReleaseStageIfActive(stages, newPushReleaseStage(inputs.ProjectRoot, inputs.ReleaseConfig, inputs.DevelopBranchExists))
	return nextVersion, stages, nil
}

func newStableBumpStage(inputs releaseInputs, nextVersion string) ReleaseStage {
	if strings.TrimSpace(inputs.VersionFilePath) == "" || nextVersion == inputs.BaseVersion {
		return ReleaseStage{}
	}
	bumpUpdate := ReleaseFileUpdate{
		Path:    inputs.VersionFilePath,
		Content: nextVersion + "\n",
	}
	return newBumpStage(inputs.ProjectRoot, nextVersion, bumpUpdate)
}

func candidatePostReleaseStages(inputs releaseInputs) []ReleaseStage {
	if inputs.Mode != ReleaseModeCandidate {
		return nil
	}
	return appendReleaseStageIfActive(nil, newPushCandidateReleaseStage(inputs.ProjectRoot, inputs.ReleaseConfig))
}

func appendReleaseStageIfActive(stages []ReleaseStage, stage ReleaseStage) []ReleaseStage {
	if len(stage.FileUpdates) == 0 && len(stage.GitCommands) == 0 && stage.PackagingSync == nil {
		return stages
	}
	return append(stages, stage)
}

func resolveReleaseProjectRoot(findProjectRoot ProjectFinderFunc, params ReleaseParams) (string, error) {
	if projectRoot := strings.TrimSpace(params.ProjectRoot); projectRoot != "" {
		return filepath.Clean(projectRoot), nil
	}
	_, projectRoot, err := findProjectRoot()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(projectRoot) == "" {
		return "", ErrNotInGitRepository
	}
	return filepath.Clean(projectRoot), nil
}

func classifyReleaseMode(branch string, config ReleaseConfig) ReleaseMode {
	branch = strings.TrimSpace(branch)
	switch branch {
	case strings.TrimSpace(config.MainBranch):
		return ReleaseModeStable
	case strings.TrimSpace(config.DevelopBranch):
		return ReleaseModeCandidate
	default:
		return ReleaseModePrerelease
	}
}

func resolveReleaseVersion(baseVersion, commit string, mode ReleaseMode) string {
	baseVersion = strings.TrimSpace(baseVersion)
	commit = strings.TrimSpace(commit)

	switch mode {
	case ReleaseModeStable:
		return baseVersion
	case ReleaseModeCandidate:
		return fmt.Sprintf("%s-rc.%s", baseVersion, commit)
	default:
		return fmt.Sprintf("%s-pr.%s", baseVersion, commit)
	}
}

func discoverReleaseCharts(projectRoot, version string) ([]ReleaseChartSpec, []ReleaseFileUpdate, error) {
	chartPaths, err := findReleaseChartPaths(projectRoot)
	if err != nil {
		return nil, nil, err
	}

	charts := make([]ReleaseChartSpec, 0, len(chartPaths))
	updates := make([]ReleaseFileUpdate, 0, len(chartPaths))
	for _, chartPath := range chartPaths {
		content, changed, chartSpec, err := updateHelmChartReleaseVersion(filepath.Join(chartPath, "Chart.yaml"), version)
		if err != nil {
			return nil, nil, err
		}
		charts = append(charts, chartSpec)
		if changed {
			updates = append(updates, ReleaseFileUpdate{
				Path:    filepath.Join(chartPath, "Chart.yaml"),
				Content: content,
			})
		}
	}
	return charts, updates, nil
}

func discoverReleaseDockerImages(projectRoot, releaseRoot, versionFilePath, version string) ([]ReleaseDockerImageSpec, error) {
	dockerDir := filepath.Join(releaseRoot, "docker")
	buildContexts, err := DockerBuildContextsUnderDir(dockerDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if len(buildContexts) == 0 {
		return nil, nil
	}

	registry, err := resolveDockerBuildRegistryForEnvironment(projectRoot, "")
	if err != nil {
		return nil, err
	}

	images := make([]ReleaseDockerImageSpec, 0, len(buildContexts))
	for _, buildContext := range buildContexts {
		_, _, candidateVersionFilePath, err := ResolveDockerBuildVersion(buildContext.Dir, releaseRoot)
		if err != nil {
			return nil, err
		}
		if filepath.Clean(candidateVersionFilePath) != filepath.Clean(versionFilePath) {
			continue
		}

		imageName := strings.TrimSpace(filepath.Base(buildContext.Dir))
		tag := imageName + ":" + version
		if strings.TrimSpace(registry) != "" {
			tag = strings.TrimRight(registry, "/") + "/" + tag
		}
		images = append(images, ReleaseDockerImageSpec{
			ContextDir:     ResolveDockerBuildContextDirForProject(buildContext.Dir, releaseRoot),
			DockerfilePath: buildContext.DockerfilePath,
			ImageName:      imageName,
			Registry:       registry,
			Tag:            tag,
			Version:        version,
		})
	}
	return images, nil
}

func discoverReleaseLinuxScripts(releaseRoot, version string) ([]scriptSpec, error) {
	linuxDir := filepath.Join(releaseRoot, "linux")
	contexts, err := ResolveLinuxPackageContextsAtDir(linuxDir)
	if err != nil {
		if errors.Is(err, ErrLinuxPackageBuildNotFound) || errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	scripts := make([]scriptSpec, 0, len(contexts))
	for _, context := range contexts {
		if strings.TrimSpace(context.ReleaseScriptPath) == "" {
			continue
		}
		scripts = append(scripts, newScriptSpec(context.Dir, "./release.sh", version))
	}
	return scripts, nil
}

func discoverStableReleasePackaging(projectRoot, version string) ([]ReleaseFileUpdate, *ReleasePackagingSyncSpec, error) {
	updates := make([]ReleaseFileUpdate, 0, 2)
	syncSpec := &ReleasePackagingSyncSpec{Version: version}

	formulaPath := filepath.Join(projectRoot, "Formula", "erun.rb")
	formulaContent, formulaChanged, err := updateHomebrewFormulaReleaseVersion(formulaPath, version)
	if err != nil {
		return nil, nil, err
	}
	if formulaChanged {
		updates = append(updates, ReleaseFileUpdate{
			Path:    formulaPath,
			Content: formulaContent,
		})
	}
	if fileExists(formulaPath) {
		syncSpec.FormulaPath = formulaPath
	}

	scoopPath := filepath.Join(projectRoot, "bucket", "erun.json")
	scoopContent, scoopChanged, err := updateScoopManifestReleaseVersion(scoopPath, version)
	if err != nil {
		return nil, nil, err
	}
	if scoopChanged {
		updates = append(updates, ReleaseFileUpdate{
			Path:    scoopPath,
			Content: scoopContent,
		})
	}
	if fileExists(scoopPath) {
		syncSpec.ScoopPath = scoopPath
	}
	if syncSpec.FormulaPath == "" && syncSpec.ScoopPath == "" {
		return updates, nil, nil
	}

	return updates, syncSpec, nil
}

func updateHelmChartReleaseVersion(chartFilePath, version string) (string, bool, ReleaseChartSpec, error) {
	data, err := os.ReadFile(chartFilePath)
	if err != nil {
		return "", false, ReleaseChartSpec{}, err
	}

	var chart map[string]interface{}
	if err := yaml.Unmarshal(data, &chart); err != nil {
		return "", false, ReleaseChartSpec{}, err
	}
	if chart == nil {
		return "", false, ReleaseChartSpec{}, errors.New("chart.yaml is empty")
	}

	changed := false
	if strings.TrimSpace(fmt.Sprint(chart["version"])) != version {
		chart["version"] = version
		changed = true
	}
	if strings.TrimSpace(fmt.Sprint(chart["appVersion"])) != version {
		chart["appVersion"] = version
		changed = true
	}

	updated, err := yaml.Marshal(chart)
	if err != nil {
		return "", false, ReleaseChartSpec{}, err
	}

	return string(updated), changed, ReleaseChartSpec{
		ChartPath:  filepath.Dir(chartFilePath),
		Version:    version,
		AppVersion: version,
	}, nil
}

func updateHomebrewFormulaReleaseVersion(formulaPath, version string) (string, bool, error) {
	data, err := os.ReadFile(formulaPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}

	content := string(data)
	wantURL := `url "https://github.com/sophium/erun/archive/refs/tags/v` + version + `.tar.gz"`
	urlPattern := regexp.MustCompile(`(?m)^  url "https://github\.com/sophium/erun/archive/refs/tags/v[^"]+\.tar\.gz"$`)
	updated := urlPattern.ReplaceAllString(content, "  "+wantURL)
	if updated == content {
		return "", false, nil
	}

	return updated, true, nil
}

func updateScoopManifestReleaseVersion(manifestPath, version string) (string, bool, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}

	var manifest struct {
		Version     string   `json:"version"`
		Description string   `json:"description"`
		Homepage    string   `json:"homepage"`
		License     string   `json:"license"`
		Depends     []string `json:"depends,omitempty"`
		URL         string   `json:"url"`
		Hash        string   `json:"hash"`
		ExtractDir  string   `json:"extract_dir"`
		Installer   struct {
			Script []string `json:"script,omitempty"`
		} `json:"installer,omitempty"`
		Bin []string `json:"bin,omitempty"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", false, err
	}

	wantURL := "https://github.com/sophium/erun/archive/refs/tags/v" + version + ".zip"
	wantExtractDir := "erun-" + version
	if manifest.Version == version && manifest.URL == wantURL && manifest.ExtractDir == wantExtractDir {
		return "", false, nil
	}

	manifest.Version = version
	manifest.URL = wantURL
	manifest.ExtractDir = wantExtractDir

	updated, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", false, err
	}

	return string(updated) + "\n", true, nil
}

func updateHomebrewFormulaReleaseChecksum(formulaPath, checksum string) (string, bool, error) {
	data, err := os.ReadFile(formulaPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}

	content := string(data)
	want := `sha256 "` + checksum + `"`
	pattern := regexp.MustCompile(`(?m)^  sha256 "[0-9a-f]+"$`)
	updated := pattern.ReplaceAllString(content, "  "+want)
	if updated == content {
		return "", false, nil
	}

	return updated, true, nil
}

func updateScoopManifestReleaseChecksum(manifestPath, checksum string) (string, bool, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}

	var manifest struct {
		Version     string   `json:"version"`
		Description string   `json:"description"`
		Homepage    string   `json:"homepage"`
		License     string   `json:"license"`
		Depends     []string `json:"depends,omitempty"`
		URL         string   `json:"url"`
		Hash        string   `json:"hash"`
		ExtractDir  string   `json:"extract_dir"`
		Installer   struct {
			Script []string `json:"script,omitempty"`
		} `json:"installer,omitempty"`
		Bin []string `json:"bin,omitempty"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", false, err
	}
	if manifest.Hash == checksum {
		return "", false, nil
	}
	manifest.Hash = checksum
	updated, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", false, err
	}
	return string(updated) + "\n", true, nil
}

func syncReleasePackagingChecksums(ctx Context, spec ReleasePackagingSyncSpec) ([]ReleaseFileUpdate, error) {
	if spec.FormulaPath == "" && spec.ScoopPath == "" {
		return nil, nil
	}

	updates := make([]ReleaseFileUpdate, 0, 2)
	formulaUpdate, ok, err := syncHomebrewReleaseChecksum(ctx, spec)
	if err != nil {
		return nil, err
	}
	if ok {
		updates = append(updates, formulaUpdate)
	}
	scoopUpdate, ok, err := syncScoopReleaseChecksum(ctx, spec)
	if err != nil {
		return nil, err
	}
	if ok {
		updates = append(updates, scoopUpdate)
	}
	return updates, nil
}

func syncHomebrewReleaseChecksum(ctx Context, spec ReleasePackagingSyncSpec) (ReleaseFileUpdate, bool, error) {
	if spec.FormulaPath == "" {
		return ReleaseFileUpdate{}, false, nil
	}
	url := "https://github.com/sophium/erun/archive/refs/tags/v" + spec.Version + ".tar.gz"
	ctx.TraceCommand("", "curl", "-fsSL", url)
	ctx.TraceCommand("", "shasum", "-a", "256", "v"+spec.Version+".tar.gz")
	if ctx.DryRun {
		return ReleaseFileUpdate{}, false, nil
	}
	checksum, err := releaseArchiveSHA256(url)
	if err != nil {
		return ReleaseFileUpdate{}, false, err
	}
	content, changed, err := updateHomebrewFormulaReleaseChecksum(spec.FormulaPath, checksum)
	if err != nil || !changed {
		return ReleaseFileUpdate{}, false, err
	}
	return ReleaseFileUpdate{Path: spec.FormulaPath, Content: content}, true, nil
}

func syncScoopReleaseChecksum(ctx Context, spec ReleasePackagingSyncSpec) (ReleaseFileUpdate, bool, error) {
	if spec.ScoopPath == "" {
		return ReleaseFileUpdate{}, false, nil
	}
	url := "https://github.com/sophium/erun/archive/refs/tags/v" + spec.Version + ".zip"
	ctx.TraceCommand("", "curl", "-fsSL", url)
	ctx.TraceCommand("", "shasum", "-a", "256", "v"+spec.Version+".zip")
	if ctx.DryRun {
		return ReleaseFileUpdate{}, false, nil
	}
	checksum, err := releaseArchiveSHA256(url)
	if err != nil {
		return ReleaseFileUpdate{}, false, err
	}
	content, changed, err := updateScoopManifestReleaseChecksum(spec.ScoopPath, checksum)
	if err != nil || !changed {
		return ReleaseFileUpdate{}, false, err
	}
	return ReleaseFileUpdate{Path: spec.ScoopPath, Content: content}, true, nil
}

func releaseArchiveSHA256(url string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		checksum, retry, err := fetchReleaseArchiveSHA256(client, url)
		if err == nil {
			return checksum, nil
		}
		lastErr = err
		if !retry {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("failed to download release archive %q", url)
	}
	return "", lastErr
}

func fetchReleaseArchiveSHA256(client *http.Client, url string) (string, bool, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", true, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		retry := resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError
		return "", retry, fmt.Errorf("download %q failed: %s", url, resp.Status)
	}

	sum := sha256.New()
	if _, err := io.Copy(sum, resp.Body); err != nil {
		return "", true, err
	}
	return hex.EncodeToString(sum.Sum(nil)), false, nil
}

func newReleaseStage(projectRoot string, fileUpdates []ReleaseFileUpdate, version string, mode ReleaseMode) ReleaseStage {
	stage := ReleaseStage{
		Name:        "release",
		FileUpdates: fileUpdates,
	}

	if len(fileUpdates) > 0 {
		addArgs := append([]string{"add"}, releaseUpdatedPaths(projectRoot, fileUpdates)...)
		stage.GitCommands = append(stage.GitCommands,
			releaseGitCommand(projectRoot, addArgs...),
			releaseGitCommand(projectRoot, "commit", "-m", "[skip ci] release "+version),
		)
	}

	tagMessage := "Release " + version
	if mode == ReleaseModeCandidate {
		tagMessage = "Release candidate " + version
	}
	if mode == ReleaseModePrerelease {
		tagMessage = "Prerelease " + version
	}
	stage.GitCommands = append(stage.GitCommands, releaseGitCommand(projectRoot, "tag", "-a", "v"+version, "-m", tagMessage))
	return stage
}

func newPushReleaseTagStage(projectRoot, version string) ReleaseStage {
	version = strings.TrimSpace(version)
	if version == "" {
		return ReleaseStage{}
	}

	return ReleaseStage{
		Name: "push-release-tag",
		GitCommands: []ReleaseCommandSpec{
			releaseGitCommand(projectRoot, "push", "origin", "v"+version),
		},
	}
}

func newSyncPackagingStage(projectRoot string, spec ReleasePackagingSyncSpec) ReleaseStage {
	if spec.FormulaPath == "" && spec.ScoopPath == "" {
		return ReleaseStage{}
	}

	updates := make([]ReleaseFileUpdate, 0, 2)
	if spec.FormulaPath != "" {
		updates = append(updates, ReleaseFileUpdate{Path: spec.FormulaPath})
	}
	if spec.ScoopPath != "" {
		updates = append(updates, ReleaseFileUpdate{Path: spec.ScoopPath})
	}

	addArgs := append([]string{"add"}, releaseUpdatedPaths(projectRoot, updates)...)
	return ReleaseStage{
		Name:          "sync-packaging-checksums",
		PackagingSync: &spec,
		GitCommands: []ReleaseCommandSpec{
			releaseGitCommand(projectRoot, addArgs...),
			releaseGitCommand(projectRoot, "commit", "-m", "[skip ci] sync package metadata "+spec.Version),
		},
	}
}

func newBumpStage(projectRoot, nextVersion string, fileUpdate ReleaseFileUpdate) ReleaseStage {
	return ReleaseStage{
		Name:        "post-release-version-bump",
		FileUpdates: []ReleaseFileUpdate{fileUpdate},
		GitCommands: []ReleaseCommandSpec{
			releaseGitCommand(projectRoot, "add", releaseGitPath(projectRoot, fileUpdate.Path)),
			releaseGitCommand(projectRoot, "commit", "-m", "[skip ci] prepare "+nextVersion),
		},
	}
}

func newSyncRemoteStage(projectRoot, branch string) ReleaseStage {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return ReleaseStage{}
	}

	return ReleaseStage{
		Name: "sync-remote",
		GitCommands: []ReleaseCommandSpec{
			releaseGitCommand(projectRoot, "fetch", "origin"),
			releaseGitCommand(projectRoot, "rebase", "origin/"+branch),
		},
	}
}

func newSyncDevelopStage(projectRoot string, config ReleaseConfig, developBranchExists bool) ReleaseStage {
	mainBranch := strings.TrimSpace(config.MainBranch)
	developBranch := strings.TrimSpace(config.DevelopBranch)
	if mainBranch == "" || developBranch == "" || !developBranchExists {
		return ReleaseStage{}
	}

	return ReleaseStage{
		Name: "sync-develop",
		GitCommands: []ReleaseCommandSpec{
			releaseGitCommand(projectRoot, "checkout", developBranch),
			releaseGitCommand(projectRoot, "merge", "--no-edit", "-X", "theirs", mainBranch),
			releaseGitCommand(projectRoot, "checkout", mainBranch),
		},
	}
}

func newPushReleaseStage(projectRoot string, config ReleaseConfig, developBranchExists bool) ReleaseStage {
	mainBranch := strings.TrimSpace(config.MainBranch)
	developBranch := strings.TrimSpace(config.DevelopBranch)
	if mainBranch == "" {
		return ReleaseStage{}
	}

	args := []string{"push", "--follow-tags", "origin", mainBranch}
	if developBranchExists && developBranch != "" {
		args = append(args, developBranch)
	}

	return ReleaseStage{
		Name: "push",
		GitCommands: []ReleaseCommandSpec{
			releaseGitCommand(projectRoot, args...),
		},
	}
}

func newPushCandidateReleaseStage(projectRoot string, config ReleaseConfig) ReleaseStage {
	developBranch := strings.TrimSpace(config.DevelopBranch)
	if developBranch == "" {
		return ReleaseStage{}
	}

	return ReleaseStage{
		Name: "push",
		GitCommands: []ReleaseCommandSpec{
			releaseGitCommand(projectRoot, "push", "--follow-tags", "origin", developBranch),
		},
	}
}

func releaseUpdatedPaths(projectRoot string, fileUpdates []ReleaseFileUpdate) []string {
	paths := make([]string, 0, len(fileUpdates))
	for _, update := range fileUpdates {
		paths = append(paths, releaseGitPath(projectRoot, update.Path))
	}
	return paths
}

func releaseGitCommand(dir string, args ...string) ReleaseCommandSpec {
	return ReleaseCommandSpec{
		Dir:  dir,
		Name: "git",
		Args: args,
	}
}

func nextPatchVersion(version string) (string, error) {
	parts := strings.Split(strings.TrimSpace(version), ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid semantic version %q", version)
	}

	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return "", fmt.Errorf("invalid semantic version %q", version)
	}
	parts[2] = strconv.Itoa(patch + 1)
	return strings.Join(parts, "."), nil
}

func releaseGitPath(projectRoot, path string) string {
	relative, err := filepath.Rel(projectRoot, path)
	if err != nil {
		return path
	}
	return filepath.Clean(relative)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func findReleaseChartPaths(projectRoot string) ([]string, error) {
	matches := make([]string, 0, 4)
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
		if d.Name() != "Chart.yaml" {
			return nil
		}
		matches = append(matches, filepath.Dir(path))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

func resolveReleaseModuleRoot(projectRoot string) (string, error) {
	projectRoot = filepath.Clean(strings.TrimSpace(projectRoot))

	if _, _, _, err := ResolveDockerBuildVersion(projectRoot, projectRoot); err == nil {
		return projectRoot, nil
	} else if !errors.Is(err, ErrVersionFileNotFound) {
		return "", err
	}

	roots, err := findNestedReleaseRoots(projectRoot)
	if err != nil {
		return "", err
	}
	switch len(roots) {
	case 0:
		return "", ErrVersionFileNotFound
	case 1:
		return roots[0], nil
	default:
		return "", fmt.Errorf("multiple release roots found under project root")
	}
}

func findNestedReleaseRoots(projectRoot string) ([]string, error) {
	matches := make([]string, 0, 2)
	err := filepath.WalkDir(projectRoot, func(path string, d os.DirEntry, err error) error {
		dir, ok, walkErr := nestedReleaseRootCandidate(projectRoot, path, d, err)
		if ok {
			matches = append(matches, dir)
		}
		return walkErr
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

func nestedReleaseRootCandidate(projectRoot, path string, d os.DirEntry, err error) (string, bool, error) {
	if err != nil {
		return "", false, err
	}
	if d.IsDir() {
		if d.Name() == ".git" {
			return "", false, filepath.SkipDir
		}
		return "", false, nil
	}
	if d.Name() != "VERSION" {
		return "", false, nil
	}
	dir := filepath.Dir(path)
	if dir == projectRoot {
		return "", false, nil
	}
	parts, err := releaseRootRelativeParts(projectRoot, dir)
	if err != nil || ignoredNestedReleaseRoot(parts) {
		return "", false, err
	}
	return dir, true, nil
}

func releaseRootRelativeParts(projectRoot, dir string) ([]string, error) {
	relative, err := filepath.Rel(projectRoot, dir)
	if err != nil {
		return nil, err
	}
	return strings.Split(filepath.ToSlash(relative), "/"), nil
}

func ignoredNestedReleaseRoot(parts []string) bool {
	for _, part := range parts {
		if part == "assets" {
			return true
		}
	}
	return len(parts) >= 2 && parts[1] == "docker"
}
