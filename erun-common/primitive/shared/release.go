package shared

import (
	"fmt"
	"os"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/gitx"
)

type (
	GitBranchCheckerFunc func(core.Context, string, string) (bool, error)
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
	ProjectRoot     string `json:"projectRoot,omitempty"`
	Version         string `json:"version"`
	FormulaPath     string `json:"formulaPath,omitempty"`
	ScoopPath       string `json:"scoopPath,omitempty"`
	MarketplacePath string `json:"marketplacePath,omitempty"`
}

// ReleaseSpec runs Stages while the release is still private and recoverable;
// PostPublishStages hold the irreversible pushes. `erun release` reaches them having
// published nothing, and its failures must not claim otherwise.
type ReleaseSpec struct {
	ProjectRoot       string                   `json:"projectRoot"`
	ReleaseRoot       string                   `json:"releaseRoot"`
	Branch            string                   `json:"branch"`
	Commit            string                   `json:"commit"`
	BaseVersion       string                   `json:"baseVersion"`
	Version           string                   `json:"version"`
	NextVersion       string                   `json:"nextVersion,omitempty"`
	VersionFilePath   string                   `json:"versionFilePath"`
	Mode              ReleaseMode              `json:"mode"`
	Force             bool                     `json:"force,omitempty"`
	Charts            []ReleaseChartSpec       `json:"charts,omitempty"`
	DockerImages      []ReleaseDockerImageSpec `json:"dockerImages,omitempty"`
	Stages            []ReleaseStage           `json:"stages,omitempty"`
	PostPublishStages []ReleaseStage           `json:"postPublishStages,omitempty"`
	// LinuxPackages are exported during the publish and attached once the tag is public.
	LinuxPackages   []LinuxPackageSpec `json:"linuxPackages,omitempty"`
	LinuxPackageDir string             `json:"linuxPackageDir,omitempty"`

	// ArtifactsPublished is run state, not plan; always false for `erun release`.
	ArtifactsPublished bool `json:"-"`
}

type releaseInputs struct {
	ProjectRoot         string
	ReleaseRoot         string
	ReleaseConfig       configpkg.ReleaseConfig
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
	LinuxPackages []LinuxPackageSpec
}

func ResolveReleaseSpec(ctx core.Context, findProjectRoot ProjectFinderFunc, params ReleaseParams) (ReleaseSpec, error) {
	return resolveReleaseSpec(ctx, findProjectRoot, configpkg.LoadProjectConfig, gitx.GitCurrentBranch, gitx.GitShortCommit, GitBranchExists, gitx.GitCommandRunner, params)
}

func resolveReleaseSpec(ctx core.Context, findProjectRoot ProjectFinderFunc, loadProjectConfig ProjectConfigLoaderFunc, resolveBranch, resolveCommit gitx.GitValueResolverFunc, branchExists GitBranchCheckerFunc, runGit gitx.GitCommandRunnerFunc, params ReleaseParams) (ReleaseSpec, error) {
	findProjectRoot, loadProjectConfig, resolveBranch, resolveCommit, branchExists, runGit = normalizeReleaseDependencies(findProjectRoot, loadProjectConfig, resolveBranch, resolveCommit, branchExists, runGit)

	inputs, err := resolveReleaseInputs(ctx, findProjectRoot, loadProjectConfig, resolveBranch, resolveCommit, branchExists, runGit, params)
	if err != nil {
		return ReleaseSpec{}, err
	}
	artifacts, err := discoverReleaseArtifacts(ctx, inputs)
	if err != nil {
		return ReleaseSpec{}, err
	}
	nextVersion, stages, postPublishStages, err := resolveReleaseStages(inputs, artifacts.FileUpdates, artifacts.PackagingSync)
	if err != nil {
		return ReleaseSpec{}, err
	}

	return ReleaseSpec{
		ProjectRoot:       inputs.ProjectRoot,
		ReleaseRoot:       inputs.ReleaseRoot,
		Branch:            inputs.Branch,
		Commit:            inputs.Commit,
		BaseVersion:       inputs.BaseVersion,
		Version:           inputs.Version,
		NextVersion:       nextVersion,
		VersionFilePath:   inputs.VersionFilePath,
		Mode:              inputs.Mode,
		Force:             params.Force,
		Charts:            artifacts.Charts,
		DockerImages:      artifacts.Images,
		Stages:            stages,
		PostPublishStages: postPublishStages,
		LinuxPackages:     artifacts.LinuxPackages,
		LinuxPackageDir:   linuxPackageDirForRelease(inputs, artifacts.LinuxPackages),
	}, nil
}

type ReleasePackagingSyncerFunc func(core.Context, ReleasePackagingSyncSpec) ([]ReleaseFileUpdate, error)

func RunReleaseSpec(ctx core.Context, spec ReleaseSpec, runGit gitx.GitCommandRunnerFunc, syncPackagingChecksums ReleasePackagingSyncerFunc, publisher *ReleasePublisher) error {
	if runGit == nil {
		runGit = gitx.GitCommandRunner
	}
	if syncPackagingChecksums == nil {
		syncPackagingChecksums = syncReleasePackagingChecksums
	}

	release, err := ClaimReleaseVersion(ctx, spec, os.Getenv)
	if err != nil {
		return err
	}
	defer release()

	return RunClaimedReleaseSpec(ctx, spec, runGit, syncPackagingChecksums, publisher)
}

// RunClaimedReleaseSpec runs once the version is claimed. A nil publisher (erun release)
// skips everything the publish owns, so a tag never implies artifacts exist.
func RunClaimedReleaseSpec(ctx core.Context, spec ReleaseSpec, runGit gitx.GitCommandRunnerFunc, syncPackagingChecksums ReleasePackagingSyncerFunc, publisher *ReleasePublisher) error {
	traceReleaseSpec(ctx, spec)
	if err := ensureReleaseCouldPublish(spec, publisher); err != nil {
		return err
	}
	if err := ensureReleaseWorktreeClean(ctx, spec.ProjectRoot); err != nil {
		return err
	}
	if err := ensureReleaseHasDiskHeadroom(ctx, publisher); err != nil {
		return err
	}

	if err := runReleaseStages(ctx, spec, spec.Stages, runGit, syncPackagingChecksums); err != nil {
		return err
	}
	if err := publishClaimedRelease(ctx, spec, runGit, publisher); err != nil {
		return err
	}
	if publisher != nil && publisher.Publish != nil {
		spec.ArtifactsPublished = true
	}
	if err := runReleaseStages(ctx, spec, spec.PostPublishStages, runGit, syncPackagingChecksums); err != nil {
		return err
	}
	return uploadPublishedLinuxPackages(ctx, spec, publisher)
}

// uploadPublishedLinuxPackages runs only for a run that published, after the tag push.
func uploadPublishedLinuxPackages(ctx core.Context, spec ReleaseSpec, publisher *ReleasePublisher) error {
	if publisher == nil || publisher.Publish == nil {
		return nil
	}
	return UploadReleaseLinuxPackages(ctx, spec)
}

// ensureReleaseHasDiskHeadroom runs before the release stage, so a refusal leaves no
// version commit or tag behind. A nil publisher builds nothing and skips it.
func ensureReleaseHasDiskHeadroom(ctx core.Context, publisher *ReleasePublisher) error {
	if publisher == nil {
		return nil
	}
	return ensureReleaseDiskHeadroom(ctx)
}

func ensureReleaseCouldPublish(spec ReleaseSpec, publisher *ReleasePublisher) error {
	if publisher == nil {
		return nil
	}
	return ensureReleasePublishesResolvedImages(spec, *publisher)
}

// publishClaimedRelease publishes, or says nothing was published (erun release).
func publishClaimedRelease(ctx core.Context, spec ReleaseSpec, runGit gitx.GitCommandRunnerFunc, publisher *ReleasePublisher) error {
	if publisher == nil {
		if spec.Version != "" {
			ctx.Info("release version: " + spec.Version + " (source control only; no artifacts were built or published)")
			ctx.Info("next: `erun build --release` publishes this version's images and charts before tagging, or `erun push --version " + spec.Version + "` publishes them for the tag already pushed")
		}
		return nil
	}
	// Re-checked as late as possible, right before the build spends.
	if err := ensureReleaseBaseBranchUnmoved(ctx, spec, runGit); err != nil {
		return err
	}
	return runReleasePublication(ctx, *publisher)
}

func runReleaseStages(ctx core.Context, spec ReleaseSpec, stages []ReleaseStage, runGit gitx.GitCommandRunnerFunc, syncPackagingChecksums ReleasePackagingSyncerFunc) error {
	for _, stage := range stages {
		stepCtx, finish := ctx.StartTimingStep(stage.Name)
		err := runReleaseStage(stepCtx, spec, stage, runGit, syncPackagingChecksums)
		finish(err)
		if err != nil {
			return err
		}
	}
	return nil
}

func traceReleaseSpec(ctx core.Context, spec ReleaseSpec) {
	ctx.Trace(fmt.Sprintf("release: branch=%s mode=%s version=%s", spec.Branch, spec.Mode, spec.Version))
	if spec.NextVersion != "" {
		ctx.Trace("next version: " + spec.NextVersion)
	}
	for _, image := range spec.DockerImages {
		ctx.Trace("publishing image: " + image.Tag)
	}
}

func ensureReleaseWorktreeClean(ctx core.Context, projectRoot string) error {
	clean, err := GitWorktreeClean(ctx, projectRoot)
	if err != nil {
		return err
	}
	ctx.Trace(fmt.Sprintf("release: worktree clean = %v", clean))
	if clean || ctx.DryRun {
		return nil
	}
	return fmt.Errorf("release requires a clean git worktree; commit or stash changes first")
}

func runReleaseStage(ctx core.Context, spec ReleaseSpec, stage ReleaseStage, runGit gitx.GitCommandRunnerFunc, syncPackagingChecksums ReleasePackagingSyncerFunc) error {
	ctx.Trace("stage: " + stage.Name)
	stageFileUpdates, err := releaseStageFileUpdates(ctx, stage, syncPackagingChecksums)
	if err != nil {
		return err
	}
	if err := writeReleaseFileUpdates(ctx, stageFileUpdates); err != nil {
		return err
	}
	for _, command := range releaseStageCommands(ctx, stage, stageFileUpdates) {
		if err := RunReleaseCommand(ctx, spec, stage, command, runGit); err != nil {
			return err
		}
	}
	return nil
}

func releaseStageFileUpdates(ctx core.Context, stage ReleaseStage, syncPackagingChecksums ReleasePackagingSyncerFunc) ([]ReleaseFileUpdate, error) {
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

func writeReleaseFileUpdates(ctx core.Context, updates []ReleaseFileUpdate) error {
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

func releaseStageCommands(ctx core.Context, stage ReleaseStage, updates []ReleaseFileUpdate) []ReleaseCommandSpec {
	if !ctx.DryRun && stage.PackagingSync != nil && len(updates) == 0 {
		return nil
	}
	return stage.GitCommands
}

func RunReleaseCommand(ctx core.Context, spec ReleaseSpec, stage ReleaseStage, command ReleaseCommandSpec, runGit gitx.GitCommandRunnerFunc) error {
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
	branchPush := isReleaseBranchPush(stage, command)
	if branchPush && ctx.DryRun {
		ctx.Trace(fmt.Sprintf("release: a push rejected by a branch that moved during the release is rebased onto origin/%s and retried (up to %d attempts)", spec.Branch, releasePushRebaseAttempts))
	}
	ctx.TraceCommand(command.Dir, command.Name, command.Args...)
	if ctx.DryRun {
		return nil
	}
	if command.Name != "git" {
		return fmt.Errorf("unsupported release command %q", command.Name)
	}
	if branchPush {
		return RunReleaseBranchPush(ctx, spec, command, runGit)
	}
	return runReleaseStageCommand(ctx, command, runGit)
}

// runReleaseStageCommand captures a push's stderr, the only stream GitHub's bypass notice uses.
func runReleaseStageCommand(ctx core.Context, command ReleaseCommandSpec, runGit gitx.GitCommandRunnerFunc) error {
	if !isGitPushArgs(command.Args) {
		return runGit(command.Dir, ctx.Stdout, ctx.Stderr, command.Args...)
	}
	// The tag push needs the same bypass capture as the branch push.
	var pushOutput strings.Builder
	return runReleasePush(ctx, command.Dir, runGit, &pushOutput, command.Args...)
}

func FileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
