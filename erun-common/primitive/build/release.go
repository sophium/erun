package build

import (
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-common/primitive/shared/projectseam"
)

type (
	GitBranchCheckerFunc func(core.Context, string, string) (bool, error)
)

type ReleaseParams struct {
	ProjectRoot string
	Force       bool
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
	Mode                model.ReleaseMode
	DevelopBranchExists bool
}

type releaseArtifacts struct {
	Charts        []model.ReleaseChartSpec
	Images        []model.ReleaseDockerImageSpec
	FileUpdates   []model.ReleaseFileUpdate
	PackagingSync *model.ReleasePackagingSyncSpec
	LinuxPackages []model.LinuxPackageSpec
}

func ResolveReleaseSpec(ctx core.Context, findProjectRoot projectseam.ProjectFinderFunc, params ReleaseParams) (model.ReleaseSpec, error) {
	return resolveReleaseSpec(ctx, findProjectRoot, configpkg.LoadProjectConfig, gitx.GitCurrentBranch, gitx.GitShortCommit, GitBranchExists, gitx.GitCommandRunner, params)
}

func resolveReleaseSpec(ctx core.Context, findProjectRoot projectseam.ProjectFinderFunc, loadProjectConfig projectseam.ProjectConfigLoaderFunc, resolveBranch, resolveCommit gitx.GitValueResolverFunc, branchExists GitBranchCheckerFunc, runGit gitx.GitCommandRunnerFunc, params ReleaseParams) (model.ReleaseSpec, error) {
	findProjectRoot, loadProjectConfig, resolveBranch, resolveCommit, branchExists, runGit = normalizeReleaseDependencies(findProjectRoot, loadProjectConfig, resolveBranch, resolveCommit, branchExists, runGit)

	inputs, err := resolveReleaseInputs(ctx, findProjectRoot, loadProjectConfig, resolveBranch, resolveCommit, branchExists, runGit, params)
	if err != nil {
		return model.ReleaseSpec{}, err
	}
	artifacts, err := discoverReleaseArtifacts(ctx, inputs)
	if err != nil {
		return model.ReleaseSpec{}, err
	}
	nextVersion, stages, postPublishStages, err := resolveReleaseStages(inputs, artifacts.FileUpdates, artifacts.PackagingSync)
	if err != nil {
		return model.ReleaseSpec{}, err
	}

	return model.ReleaseSpec{
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
