package build

import (
	"fmt"
	"strconv"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/shared/releaseflow"
)

// resolveReleaseStages splits the plan at the publish: the recoverable local commit and tag
// first, everything that makes the version public after the artifacts exist.
func resolveReleaseStages(inputs releaseInputs, releaseFileUpdates []model.ReleaseFileUpdate, packagingSync *model.ReleasePackagingSyncSpec) (string, []model.ReleaseStage, []model.ReleaseStage, error) {
	stages := baseReleaseStages(inputs, releaseFileUpdates)
	postPublishStages := stablePackagingStages(inputs, packagingSync)
	nextVersion, stableStages, err := stablePostReleaseStages(inputs)
	if err != nil {
		return "", nil, nil, err
	}
	postPublishStages = append(postPublishStages, stableStages...)
	postPublishStages = append(postPublishStages, candidatePostReleaseStages(inputs)...)
	return nextVersion, stages, postPublishStages, nil
}

func baseReleaseStages(inputs releaseInputs, releaseFileUpdates []model.ReleaseFileUpdate) []model.ReleaseStage {
	stages := make([]model.ReleaseStage, 0, 2)
	stages = appendReleaseStageIfActive(stages, newSyncRemoteStage(inputs.ProjectRoot, inputs.Branch))
	stages = appendReleaseStageIfActive(stages, releaseflow.NewReleaseStage(inputs.ProjectRoot, releaseFileUpdates, inputs.Version, inputs.Mode))
	return stages
}

func stablePackagingStages(inputs releaseInputs, packagingSync *model.ReleasePackagingSyncSpec) []model.ReleaseStage {
	if inputs.Mode != model.ReleaseModeStable || packagingSync == nil {
		return nil
	}
	stages := make([]model.ReleaseStage, 0, 2)
	stages = appendReleaseStageIfActive(stages, NewPushReleaseTagStage(inputs.ProjectRoot, inputs.Version))
	stages = appendReleaseStageIfActive(stages, newSyncPackagingStage(inputs.ProjectRoot, *packagingSync))
	return stages
}

func stablePostReleaseStages(inputs releaseInputs) (string, []model.ReleaseStage, error) {
	if inputs.Mode != model.ReleaseModeStable {
		return "", nil, nil
	}
	nextVersion, err := nextPatchVersion(inputs.BaseVersion)
	if err != nil {
		return "", nil, err
	}
	stages := make([]model.ReleaseStage, 0, 3)
	stages = appendReleaseStageIfActive(stages, newStableBumpStage(inputs, nextVersion))
	stages = appendReleaseStageIfActive(stages, newSyncDevelopStage(inputs.ProjectRoot, inputs.ReleaseConfig, inputs.DevelopBranchExists))
	stages = appendReleaseStageIfActive(stages, NewPushReleaseStage(inputs.ProjectRoot, inputs.ReleaseConfig, inputs.DevelopBranchExists))
	return nextVersion, stages, nil
}

func newStableBumpStage(inputs releaseInputs, nextVersion string) model.ReleaseStage {
	if strings.TrimSpace(inputs.VersionFilePath) == "" || nextVersion == inputs.BaseVersion {
		return model.ReleaseStage{}
	}
	bumpUpdate := model.ReleaseFileUpdate{
		Path:    inputs.VersionFilePath,
		Content: nextVersion + "\n",
	}
	return newBumpStage(inputs.ProjectRoot, nextVersion, bumpUpdate)
}

func candidatePostReleaseStages(inputs releaseInputs) []model.ReleaseStage {
	if inputs.Mode != model.ReleaseModeCandidate {
		return nil
	}
	return appendReleaseStageIfActive(nil, newPushCandidateReleaseStage(inputs.ProjectRoot, inputs.ReleaseConfig))
}

func appendReleaseStageIfActive(stages []model.ReleaseStage, stage model.ReleaseStage) []model.ReleaseStage {
	if len(stage.FileUpdates) == 0 && len(stage.GitCommands) == 0 && stage.PackagingSync == nil {
		return stages
	}
	return append(stages, stage)
}

func NewPushReleaseTagStage(projectRoot, version string) model.ReleaseStage {
	version = strings.TrimSpace(version)
	if version == "" {
		return model.ReleaseStage{}
	}

	return model.ReleaseStage{
		Name: "push-release-tag",
		GitCommands: []model.ReleaseCommandSpec{
			releaseflow.ReleaseGitCommand(projectRoot, "push", "origin", "v"+version),
		},
	}
}

func newSyncPackagingStage(projectRoot string, spec model.ReleasePackagingSyncSpec) model.ReleaseStage {
	if spec.FormulaPath == "" && spec.ScoopPath == "" && spec.MarketplacePath == "" {
		return model.ReleaseStage{}
	}

	updates := make([]model.ReleaseFileUpdate, 0, 3)
	if spec.FormulaPath != "" {
		updates = append(updates, model.ReleaseFileUpdate{Path: spec.FormulaPath})
	}
	if spec.ScoopPath != "" {
		updates = append(updates, model.ReleaseFileUpdate{Path: spec.ScoopPath})
	}
	if spec.MarketplacePath != "" {
		updates = append(updates, model.ReleaseFileUpdate{Path: spec.MarketplacePath})
	}

	addArgs := append([]string{"add"}, releaseflow.ReleaseUpdatedPaths(projectRoot, updates)...)
	return model.ReleaseStage{
		Name:          "sync-packaging-checksums",
		PackagingSync: &spec,
		GitCommands: []model.ReleaseCommandSpec{
			releaseflow.ReleaseGitCommand(projectRoot, addArgs...),
			releaseflow.ReleaseGitCommand(projectRoot, "commit", "-m", "[skip ci] sync package metadata "+spec.Version),
		},
	}
}

func newBumpStage(projectRoot, nextVersion string, fileUpdate model.ReleaseFileUpdate) model.ReleaseStage {
	return model.ReleaseStage{
		Name:        "post-release-version-bump",
		FileUpdates: []model.ReleaseFileUpdate{fileUpdate},
		GitCommands: []model.ReleaseCommandSpec{
			releaseflow.ReleaseGitCommand(projectRoot, "add", releaseflow.ReleaseGitPath(projectRoot, fileUpdate.Path)),
			releaseflow.ReleaseGitCommand(projectRoot, "commit", "-m", "[skip ci] prepare "+nextVersion),
		},
	}
}

func newSyncRemoteStage(projectRoot, branch string) model.ReleaseStage {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return model.ReleaseStage{}
	}

	return model.ReleaseStage{
		Name: "sync-remote",
		GitCommands: []model.ReleaseCommandSpec{
			releaseflow.ReleaseGitCommand(projectRoot, "fetch", "origin"),
			releaseflow.ReleaseGitCommand(projectRoot, "rebase", "origin/"+branch),
		},
	}
}

func newSyncDevelopStage(projectRoot string, config configpkg.ReleaseConfig, developBranchExists bool) model.ReleaseStage {
	mainBranch := strings.TrimSpace(config.MainBranch)
	developBranch := strings.TrimSpace(config.DevelopBranch)
	if mainBranch == "" || developBranch == "" || !developBranchExists {
		return model.ReleaseStage{}
	}

	return model.ReleaseStage{
		Name: "sync-develop",
		GitCommands: []model.ReleaseCommandSpec{
			releaseflow.ReleaseGitCommand(projectRoot, "checkout", developBranch),
			releaseflow.ReleaseGitCommand(projectRoot, "merge", "--no-edit", "-X", "theirs", mainBranch),
			releaseflow.ReleaseGitCommand(projectRoot, "checkout", mainBranch),
		},
	}
}

func NewPushReleaseStage(projectRoot string, config configpkg.ReleaseConfig, developBranchExists bool) model.ReleaseStage {
	mainBranch := strings.TrimSpace(config.MainBranch)
	developBranch := strings.TrimSpace(config.DevelopBranch)
	if mainBranch == "" {
		return model.ReleaseStage{}
	}

	args := []string{"push", "--follow-tags", "origin", mainBranch}
	if developBranchExists && developBranch != "" {
		args = append(args, developBranch)
	}

	return model.ReleaseStage{
		Name: releaseflow.ReleasePushStageName,
		GitCommands: []model.ReleaseCommandSpec{
			releaseflow.ReleaseGitCommand(projectRoot, args...),
		},
	}
}

func newPushCandidateReleaseStage(projectRoot string, config configpkg.ReleaseConfig) model.ReleaseStage {
	developBranch := strings.TrimSpace(config.DevelopBranch)
	if developBranch == "" {
		return model.ReleaseStage{}
	}

	return model.ReleaseStage{
		Name: releaseflow.ReleasePushStageName,
		GitCommands: []model.ReleaseCommandSpec{
			releaseflow.ReleaseGitCommand(projectRoot, "push", "--follow-tags", "origin", developBranch),
		},
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
