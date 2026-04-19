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
	Charts          []ReleaseChartSpec       `json:"charts,omitempty"`
	DockerImages    []ReleaseDockerImageSpec `json:"dockerImages,omitempty"`
	Stages          []ReleaseStage           `json:"stages,omitempty"`
	LinuxReleases   []scriptSpec
	SkippedLinux    bool `json:"-"`
}

func ResolveReleaseSpec(findProjectRoot ProjectFinderFunc, params ReleaseParams) (ReleaseSpec, error) {
	return resolveReleaseSpec(findProjectRoot, LoadProjectConfig, GitCurrentBranch, GitShortCommit, GitLocalBranchExists, params)
}

func resolveReleaseSpec(findProjectRoot ProjectFinderFunc, loadProjectConfig ProjectConfigLoaderFunc, resolveBranch, resolveCommit GitValueResolverFunc, branchExists GitBranchCheckerFunc, params ReleaseParams) (ReleaseSpec, error) {
	findProjectRoot, loadProjectConfig, resolveBranch, resolveCommit, branchExists = normalizeReleaseDependencies(findProjectRoot, loadProjectConfig, resolveBranch, resolveCommit, branchExists)

	projectRoot, err := resolveReleaseProjectRoot(findProjectRoot, params)
	if err != nil {
		return ReleaseSpec{}, err
	}
	releaseRoot, err := resolveReleaseModuleRoot(projectRoot)
	if err != nil {
		return ReleaseSpec{}, err
	}

	projectConfig, _, err := loadProjectConfig(projectRoot)
	if err != nil && !errors.Is(err, ErrNotInitialized) {
		return ReleaseSpec{}, err
	}
	releaseConfig := projectConfig.NormalizedReleaseConfig()

	branch, err := resolveBranch(projectRoot)
	if err != nil {
		return ReleaseSpec{}, err
	}
	commit, err := resolveCommit(projectRoot)
	if err != nil {
		return ReleaseSpec{}, err
	}

	baseVersion, _, versionFilePath, err := ResolveDockerBuildVersion(releaseRoot, releaseRoot)
	if err != nil {
		return ReleaseSpec{}, err
	}

	mode := classifyReleaseMode(branch, releaseConfig)
	version := resolveReleaseVersion(baseVersion, commit, mode)
	developBranchExists, err := branchExists(projectRoot, releaseConfig.DevelopBranch)
	if err != nil {
		return ReleaseSpec{}, err
	}

	charts, chartUpdates, err := discoverReleaseCharts(releaseRoot, version)
	if err != nil {
		return ReleaseSpec{}, err
	}
	releaseFileUpdates := append([]ReleaseFileUpdate{}, chartUpdates...)
	var packagingSync *ReleasePackagingSyncSpec
	if mode == ReleaseModeStable {
		packagingUpdates, syncSpec, err := discoverStableReleasePackaging(projectRoot, version)
		if err != nil {
			return ReleaseSpec{}, err
		}
		releaseFileUpdates = append(releaseFileUpdates, packagingUpdates...)
		packagingSync = syncSpec
	}

	images, err := discoverReleaseDockerImages(projectRoot, releaseRoot, versionFilePath, version)
	if err != nil {
		return ReleaseSpec{}, err
	}
	linuxReleases, err := discoverReleaseLinuxScripts(releaseRoot, version)
	if err != nil {
		return ReleaseSpec{}, err
	}
	skippedLinux := false
	if len(linuxReleases) > 0 && !LinuxPackageBuildsSupported() {
		linuxReleases = nil
		skippedLinux = true
	}

	stages := make([]ReleaseStage, 0, 2)
	if syncStage := newSyncRemoteStage(projectRoot, branch); len(syncStage.GitCommands) > 0 {
		stages = append(stages, syncStage)
	}
	releaseStage := newReleaseStage(projectRoot, releaseFileUpdates, version, mode)
	if len(releaseStage.FileUpdates) > 0 || len(releaseStage.GitCommands) > 0 {
		stages = append(stages, releaseStage)
	}
	if mode == ReleaseModeStable && packagingSync != nil {
		tagPushStage := newPushReleaseTagStage(projectRoot, version)
		if len(tagPushStage.GitCommands) > 0 {
			stages = append(stages, tagPushStage)
		}
		packagingStage := newSyncPackagingStage(projectRoot, *packagingSync)
		if packagingStage.PackagingSync != nil || len(packagingStage.GitCommands) > 0 {
			stages = append(stages, packagingStage)
		}
	}

	nextVersion := ""
	if mode == ReleaseModeStable {
		nextVersion, err = nextPatchVersion(baseVersion)
		if err != nil {
			return ReleaseSpec{}, err
		}
		if strings.TrimSpace(versionFilePath) != "" && nextVersion != baseVersion {
			bumpUpdate := ReleaseFileUpdate{
				Path:    versionFilePath,
				Content: nextVersion + "\n",
			}
			bumpStage := newBumpStage(projectRoot, nextVersion, bumpUpdate)
			if len(bumpStage.FileUpdates) > 0 || len(bumpStage.GitCommands) > 0 {
				stages = append(stages, bumpStage)
			}
		}
		syncStage := newSyncDevelopStage(projectRoot, releaseConfig, developBranchExists)
		if len(syncStage.FileUpdates) > 0 || len(syncStage.GitCommands) > 0 {
			stages = append(stages, syncStage)
		}
		pushStage := newPushReleaseStage(projectRoot, releaseConfig, developBranchExists)
		if len(pushStage.FileUpdates) > 0 || len(pushStage.GitCommands) > 0 {
			stages = append(stages, pushStage)
		}
	}
	if mode == ReleaseModeCandidate {
		pushStage := newPushCandidateReleaseStage(projectRoot, releaseConfig)
		if len(pushStage.FileUpdates) > 0 || len(pushStage.GitCommands) > 0 {
			stages = append(stages, pushStage)
		}
	}

	return ReleaseSpec{
		ProjectRoot:     projectRoot,
		ReleaseRoot:     releaseRoot,
		Branch:          branch,
		Commit:          commit,
		BaseVersion:     baseVersion,
		Version:         version,
		NextVersion:     nextVersion,
		VersionFilePath: versionFilePath,
		Mode:            mode,
		Charts:          charts,
		DockerImages:    images,
		Stages:          stages,
		LinuxReleases:   linuxReleases,
		SkippedLinux:    skippedLinux,
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

	ctx.Trace(fmt.Sprintf("release: branch=%s mode=%s version=%s", spec.Branch, spec.Mode, spec.Version))
	if spec.NextVersion != "" {
		ctx.Trace("next version: " + spec.NextVersion)
	}
	for _, image := range spec.DockerImages {
		ctx.Trace("docker image: " + image.Tag)
	}
	if !ctx.DryRun {
		clean, err := gitWorktreeClean(spec.ProjectRoot)
		if err != nil {
			return err
		}
		if !clean {
			return fmt.Errorf("release requires a clean git worktree; commit or stash changes first")
		}
	}

	for _, stage := range spec.Stages {
		ctx.Trace("stage: " + stage.Name)
		stageFileUpdates := append([]ReleaseFileUpdate{}, stage.FileUpdates...)
		if stage.PackagingSync != nil {
			generatedUpdates, err := syncPackagingChecksums(ctx, *stage.PackagingSync)
			if err != nil {
				return err
			}
			stageFileUpdates = append(stageFileUpdates, generatedUpdates...)
		}

		for _, update := range stageFileUpdates {
			ctx.TraceBlock("write "+update.Path, strings.TrimRight(update.Content, "\n"))
			if ctx.DryRun {
				continue
			}
			if err := os.WriteFile(update.Path, []byte(update.Content), 0o644); err != nil {
				return err
			}
		}

		stageCommands := stage.GitCommands
		if !ctx.DryRun && stage.PackagingSync != nil && len(stageFileUpdates) == 0 {
			stageCommands = nil
		}
		for _, command := range stageCommands {
			ctx.TraceCommand(command.Dir, command.Name, command.Args...)
			if ctx.DryRun {
				continue
			}
			if command.Name != "git" {
				return fmt.Errorf("unsupported release command %q", command.Name)
			}
			if shouldSkipExistingReleaseTag(command.Args) {
				skip, err := canSkipExistingReleaseTag(command.Dir, command.Args[2])
				if err != nil {
					return err
				}
				if skip {
					ctx.Trace("release tag already exists at HEAD; skipping " + command.Args[2])
					continue
				}
			}
			if err := runGit(command.Dir, ctx.Stdout, ctx.Stderr, command.Args...); err != nil {
				return err
			}
		}
	}
	if spec.SkippedLinux {
		ctx.Trace("skipping linux package scripts: host is not Linux or dpkg-deb is unavailable")
	}

	if err := runScriptSpecs(ctx, spec.LinuxReleases, runScript); err != nil {
		return err
	}

	return nil
}

func shouldSkipExistingReleaseTag(args []string) bool {
	return len(args) >= 3 && args[0] == "tag" && args[1] == "-a" && strings.TrimSpace(args[2]) != ""
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

	if spec.FormulaPath != "" {
		url := "https://github.com/sophium/erun/archive/refs/tags/v" + spec.Version + ".tar.gz"
		ctx.TraceCommand("", "curl", "-fsSL", url)
		ctx.TraceCommand("", "shasum", "-a", "256", "v"+spec.Version+".tar.gz")
		if !ctx.DryRun {
			checksum, err := releaseArchiveSHA256(url)
			if err != nil {
				return nil, err
			}
			content, changed, err := updateHomebrewFormulaReleaseChecksum(spec.FormulaPath, checksum)
			if err != nil {
				return nil, err
			}
			if changed {
				updates = append(updates, ReleaseFileUpdate{Path: spec.FormulaPath, Content: content})
			}
		}
	}

	if spec.ScoopPath != "" {
		url := "https://github.com/sophium/erun/archive/refs/tags/v" + spec.Version + ".zip"
		ctx.TraceCommand("", "curl", "-fsSL", url)
		ctx.TraceCommand("", "shasum", "-a", "256", "v"+spec.Version+".zip")
		if !ctx.DryRun {
			checksum, err := releaseArchiveSHA256(url)
			if err != nil {
				return nil, err
			}
			content, changed, err := updateScoopManifestReleaseChecksum(spec.ScoopPath, checksum)
			if err != nil {
				return nil, err
			}
			if changed {
				updates = append(updates, ReleaseFileUpdate{Path: spec.ScoopPath, Content: content})
			}
		}
	}

	return updates, nil
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
	defer resp.Body.Close()

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
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "VERSION" {
			return nil
		}

		dir := filepath.Dir(path)
		if dir == projectRoot {
			return nil
		}
		relative, err := filepath.Rel(projectRoot, dir)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(relative), "/")
		for _, part := range parts {
			if part == "assets" {
				return nil
			}
		}
		if len(parts) >= 2 && parts[1] == "docker" {
			return nil
		}
		matches = append(matches, dir)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}
