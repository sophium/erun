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

// ReleaseSpec splits its stages around the publish deliberately. Stages run
// while the release is still private and every step is recoverable;
// PostPublishStages carry the outward-facing, irreversible steps (the tag push,
// the branch push, and the packaging sync that depends on the public tag).
//
// A run that publishes reaches them only once the version's images and charts
// are published and proven resolvable, and ArtifactsPublished records that it
// did. `erun release` marks source control only, so it reaches the same stages
// having published nothing at all — the split is around a publish it does not
// perform — and a failure there has to say so rather than congratulate the
// operator on a registry it never wrote to.
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
	// LinuxPackages are the linux package components the release exports
	// (their `dist` stage, every release platform) into LinuxPackageDir during
	// the publish, and attaches to the GitHub release once the tag is public.
	LinuxPackages   []LinuxPackageSpec `json:"linuxPackages,omitempty"`
	LinuxPackageDir string             `json:"linuxPackageDir,omitempty"`

	// ArtifactsPublished records that this run built, pushed and verified the
	// version's images and charts, so the push-stage failures that come after
	// it may say the registry holds them. It is run state rather than part of
	// the resolved plan, so it is not serialized -- and it stays false for
	// `erun release`, which marks source control and never publishes.
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

// RunClaimedReleaseSpec is the release's actual work, run only once
// runReleaseSpec has claimed the version being released.
//
// publisher may be nil. erun release marks source control and nothing else, so
// it reaches these stages without one, and everything the publish owns is
// skipped: the refusal to announce a version whose resolved images this run
// would not publish, the pre-spend base-branch and disk-headroom checks, and
// the build+publish itself. That is why a release's tag is a record of the
// source it points at and never a claim that artifacts exist for it.
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
	// The publish, when this run has one, has now built, pushed and verified
	// every image and chart this version resolves, which is what the
	// post-publish stages' failures are allowed to say. `erun release` reaches
	// the same stages with no publisher and nothing published, and those
	// messages must not tell its operator otherwise.
	if publisher != nil && publisher.Publish != nil {
		spec.ArtifactsPublished = true
	}
	if err := runReleaseStages(ctx, spec, spec.PostPublishStages, runGit, syncPackagingChecksums); err != nil {
		return err
	}
	return uploadPublishedLinuxPackages(ctx, spec, publisher)
}

// uploadPublishedLinuxPackages attaches the linux packages only for a run that
// published, which is the run that exported them; the GitHub release they
// attach to needs the tag the post-publish stages pushed.
func uploadPublishedLinuxPackages(ctx core.Context, spec ReleaseSpec, publisher *ReleasePublisher) error {
	if publisher == nil || publisher.Publish == nil {
		return nil
	}
	return UploadReleaseLinuxPackages(ctx, spec)
}

// ensureReleaseHasDiskHeadroom runs the release's disk preflight before the
// release stage, the last of the refusals that precede any mutation, so a
// refusal leaves the repository exactly as it was found.
//
// It belongs there rather than immediately before the publish, where it used to
// run. By that point the "release" stage had already written the version files,
// committed the stamp and created the annotated tag, so a run refused for space
// left all three behind, unpushed — and the next attempt failed on that residue
// (`release tag "vX" already exists at <sha>, expected current HEAD <sha>`)
// instead of on the disk that was still too short. The remedy that collision
// names ("delete it with `git tag -d vX` to retry") then reapplied to a symptom,
// one fresh tag per attempt, for as long as the node stayed full. Everything
// before this point in the run is read-only, and the only thing the preflight
// itself writes is docker's own build cache, so a refusal here is
// side-effect-free.
//
// A nil publisher means the caller is marking source control only (see
// RunReleaseSpec), which builds nothing and so has no disk to size.
func ensureReleaseHasDiskHeadroom(ctx core.Context, publisher *ReleasePublisher) error {
	if publisher == nil {
		return nil
	}
	return ensureReleaseDiskHeadroom(ctx)
}

// ensureReleaseCouldPublish refuses a release whose resolved images nothing in
// this run would publish.
func ensureReleaseCouldPublish(spec ReleaseSpec, publisher *ReleasePublisher) error {
	if publisher == nil {
		return nil
	}
	return ensureReleasePublishesResolvedImages(spec, *publisher)
}

// publishClaimedRelease runs the release's own publication, or reports that
// there was none. With a publisher it first re-checks the base branch, the one
// pre-spend check that has to hold as late as possible; without one -- erun
// release, which marks source control only -- it says what did not happen, so a
// release that exits 0 having published nothing cannot read as one that
// published something.
func publishClaimedRelease(ctx core.Context, spec ReleaseSpec, runGit gitx.GitCommandRunnerFunc, publisher *ReleasePublisher) error {
	if publisher == nil {
		if spec.Version != "" {
			ctx.Info("release version: " + spec.Version + " (source control only; no artifacts were built or published)")
			ctx.Info("next: `erun build --release` publishes this version's images and charts before tagging, or `erun push --version " + spec.Version + "` publishes them for the tag already pushed")
		}
		return nil
	}
	// The base branch is re-checked here rather than earlier on purpose: this is
	// the last moment before the build spends, so the window in which it could
	// have moved is as small as the check can make it. The disk preflight,
	// which refuses without mutating anything, runs before the release stage
	// instead; see ensureReleaseHasDiskHeadroom.
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
	// Named as the publish list it is: these refs are what the release pushes
	// to the registry before it tags the version, not incidental metadata.
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
		// Part of the plan rather than the run: a real release says nothing here
		// unless the push is actually rejected, and then says it as it happens.
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

// runReleaseStageCommand runs one of a stage's git commands. A push captures
// its own stderr, because that is the only stream GitHub's bypass notice ever
// appears on; every other stage command streams straight through.
func runReleaseStageCommand(ctx core.Context, command ReleaseCommandSpec, runGit gitx.GitCommandRunnerFunc) error {
	if !isGitPushArgs(command.Args) {
		return runGit(command.Dir, ctx.Stdout, ctx.Stderr, command.Args...)
	}
	// The tag push that publishes the version is the release's other push, and
	// it is a stage command rather than the branch push -- so it takes the same
	// bypass report from the same capture rather than the plain runner, which
	// would leave the tag path silent.
	var pushOutput strings.Builder
	return runReleasePush(ctx, command.Dir, runGit, &pushOutput, command.Args...)
}

func FileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
