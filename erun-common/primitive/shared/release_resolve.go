package shared

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/gitx"
)

func normalizeReleaseDependencies(findProjectRoot ProjectFinderFunc, loadProjectConfig ProjectConfigLoaderFunc, resolveBranch, resolveCommit gitx.GitValueResolverFunc, branchExists GitBranchCheckerFunc, runGit gitx.GitCommandRunnerFunc) (ProjectFinderFunc, ProjectConfigLoaderFunc, gitx.GitValueResolverFunc, gitx.GitValueResolverFunc, GitBranchCheckerFunc, gitx.GitCommandRunnerFunc) {
	if findProjectRoot == nil {
		findProjectRoot = configpkg.FindProjectRoot
	}
	if loadProjectConfig == nil {
		loadProjectConfig = configpkg.LoadProjectConfig
	}
	if resolveBranch == nil {
		resolveBranch = gitx.GitCurrentBranch
	}
	if resolveCommit == nil {
		resolveCommit = gitx.GitShortCommit
	}
	if branchExists == nil {
		branchExists = GitBranchExists
	}
	if runGit == nil {
		runGit = gitx.GitCommandRunner
	}
	return findProjectRoot, loadProjectConfig, resolveBranch, resolveCommit, branchExists, runGit
}

func resolveReleaseInputs(ctx core.Context, findProjectRoot ProjectFinderFunc, loadProjectConfig ProjectConfigLoaderFunc, resolveBranch, resolveCommit gitx.GitValueResolverFunc, branchExists GitBranchCheckerFunc, runGit gitx.GitCommandRunnerFunc, params ReleaseParams) (releaseInputs, error) {
	ctx.Trace("release: resolving project root")
	projectRoot, err := resolveReleaseProjectRoot(findProjectRoot, params)
	if err != nil {
		ctx.Trace("release: project root resolution failed: " + err.Error())
		return releaseInputs{}, err
	}
	ctx.Trace("release: project root = " + projectRoot)

	ctx.Trace("release: resolving release module root")
	releaseRoot, err := resolveReleaseModuleRoot(projectRoot)
	if err != nil {
		ctx.Trace("release: release module root resolution failed: " + err.Error())
		return releaseInputs{}, err
	}
	ctx.Trace("release: release root = " + releaseRoot)

	ctx.Trace("release: loading release config from project")
	releaseConfig, err := loadReleaseConfig(projectRoot, loadProjectConfig)
	if err != nil {
		ctx.Trace("release: release config load failed: " + err.Error())
		return releaseInputs{}, err
	}
	ctx.Trace(fmt.Sprintf("release: main branch = %s, develop branch = %s", releaseConfig.MainBranch, releaseConfig.DevelopBranch))

	ctx.Trace("release: resolving git branch and commit")
	branch, commit, err := resolveReleaseGitState(ctx, projectRoot, resolveBranch, resolveCommit, runGit)
	if err != nil {
		ctx.Trace("release: git state resolution failed: " + err.Error())
		return releaseInputs{}, err
	}
	ctx.Trace(fmt.Sprintf("release: branch = %s, commit = %s", branch, commit))

	ctx.Trace("release: resolving base version from VERSION file")
	baseVersion, _, versionFilePath, err := ResolveDockerBuildVersion(releaseRoot, releaseRoot, "")
	if err != nil {
		ctx.Trace("release: base version resolution failed: " + err.Error())
		return releaseInputs{}, err
	}
	ctx.Trace(fmt.Sprintf("release: base version = %s (from %s)", baseVersion, versionFilePath))

	mode := classifyReleaseMode(branch, releaseConfig)
	ctx.Trace("release: classified mode = " + string(mode))
	developBranchExists, err := branchExists(ctx, projectRoot, releaseConfig.DevelopBranch)
	if err != nil {
		ctx.Trace("release: develop branch lookup failed: " + err.Error())
		return releaseInputs{}, err
	}
	ctx.Trace(fmt.Sprintf("release: develop branch %q exists = %v", releaseConfig.DevelopBranch, developBranchExists))

	return releaseInputs{
		ProjectRoot:         projectRoot,
		ReleaseRoot:         releaseRoot,
		ReleaseConfig:       releaseConfig,
		Branch:              branch,
		Commit:              commit,
		BaseVersion:         baseVersion,
		Version:             ResolveReleaseVersion(baseVersion, commit, mode),
		VersionFilePath:     versionFilePath,
		Mode:                mode,
		DevelopBranchExists: developBranchExists,
	}, nil
}

func loadReleaseConfig(projectRoot string, loadProjectConfig ProjectConfigLoaderFunc) (configpkg.ReleaseConfig, error) {
	projectConfig, _, err := loadProjectConfig(projectRoot)
	if err != nil && !errors.Is(err, configpkg.ErrNotInitialized) {
		return configpkg.ReleaseConfig{}, err
	}
	return projectConfig.NormalizedReleaseConfig(), nil
}

func resolveReleaseGitState(ctx core.Context, projectRoot string, resolveBranch, resolveCommit gitx.GitValueResolverFunc, runGit gitx.GitCommandRunnerFunc) (string, string, error) {
	branch, err := resolveBranch(ctx, projectRoot)
	if err != nil {
		return "", "", err
	}
	commit, err := ResolveReleaseVersionCommit(ctx, projectRoot, branch, resolveCommit, runGit)
	if err != nil {
		return "", "", err
	}
	return branch, commit, nil
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
		return "", configpkg.ErrNotInGitRepository
	}
	return filepath.Clean(projectRoot), nil
}

func classifyReleaseMode(branch string, config configpkg.ReleaseConfig) model.ReleaseMode {
	branch = strings.TrimSpace(branch)
	switch branch {
	case strings.TrimSpace(config.MainBranch):
		return model.ReleaseModeStable
	case strings.TrimSpace(config.DevelopBranch):
		return model.ReleaseModeCandidate
	default:
		return model.ReleaseModePrerelease
	}
}

func ResolveReleaseVersion(baseVersion, commit string, mode model.ReleaseMode) string {
	baseVersion = strings.TrimSpace(baseVersion)
	commit = strings.TrimSpace(commit)

	switch mode {
	case model.ReleaseModeStable:
		return baseVersion
	case model.ReleaseModeCandidate:
		return fmt.Sprintf("%s-rc.%s", baseVersion, commit)
	case model.ReleaseModePrerelease:
	}
	return fmt.Sprintf("%s-pr.%s", baseVersion, commit)
}

func resolveReleaseModuleRoot(projectRoot string) (string, error) {
	projectRoot = filepath.Clean(strings.TrimSpace(projectRoot))

	if _, _, _, err := ResolveDockerBuildVersion(projectRoot, projectRoot, ""); err == nil {
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
	ignored, err := loadReleaseDiscoveryIgnoreSet(projectRoot)
	if err != nil {
		return nil, err
	}
	matches := make([]string, 0, 2)
	err = filepath.WalkDir(projectRoot, func(path string, d os.DirEntry, err error) error {
		dir, ok, walkErr := nestedReleaseRootCandidate(projectRoot, path, d, err, ignored)
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

func nestedReleaseRootCandidate(projectRoot, path string, d os.DirEntry, err error, ignored *IgnoreSet) (string, bool, error) {
	if err != nil {
		return "", false, err
	}
	if d.IsDir() {
		if skip := releaseDiscoverySkipDir(projectRoot, path, d, ignored); skip {
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

// Skips .git and gitignored trees, whose vendored VERSION/Chart.yaml are not release roots.
func releaseDiscoverySkipDir(projectRoot, path string, d os.DirEntry, ignored *IgnoreSet) bool {
	if d.Name() == ".git" {
		return true
	}
	if path == projectRoot {
		return false
	}
	rel, err := filepath.Rel(projectRoot, path)
	if err != nil {
		return false
	}
	return ignored.Matches(filepath.ToSlash(rel), true)
}

// Only .gitignore applies; .dockerignore is the build-context contract.
func loadReleaseDiscoveryIgnoreSet(projectRoot string) (*IgnoreSet, error) {
	combined := &IgnoreSet{}
	root, err := LoadIgnoreFile(filepath.Join(projectRoot, ".gitignore"), "")
	if err != nil {
		return nil, err
	}
	combined.Patterns = append(combined.Patterns, root.Patterns...)
	if err := LoadNestedGitignores(projectRoot, []string{"."}, combined); err != nil {
		return nil, err
	}
	return combined, nil
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
