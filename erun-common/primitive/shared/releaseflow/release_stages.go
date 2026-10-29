package releaseflow

import (
	"path/filepath"

	"github.com/sophium/erun/erun-common/base/model"
)

func NewReleaseStage(projectRoot string, fileUpdates []model.ReleaseFileUpdate, version string, mode model.ReleaseMode) model.ReleaseStage {
	stage := model.ReleaseStage{
		Name:        "release",
		FileUpdates: fileUpdates,
	}

	if len(fileUpdates) > 0 {
		addArgs := append([]string{"add"}, ReleaseUpdatedPaths(projectRoot, fileUpdates)...)
		stage.GitCommands = append(stage.GitCommands,
			ReleaseGitCommand(projectRoot, addArgs...),
			ReleaseGitCommand(projectRoot, "commit", "-m", "[skip ci] release "+version),
		)
	}

	tagMessage := "Release " + version
	if mode == model.ReleaseModeCandidate {
		tagMessage = "Release candidate " + version
	}
	if mode == model.ReleaseModePrerelease {
		tagMessage = "Prerelease " + version
	}
	stage.GitCommands = append(stage.GitCommands, ReleaseGitCommand(projectRoot, "tag", "-a", "v"+version, "-m", tagMessage))
	return stage
}

func ReleaseUpdatedPaths(projectRoot string, fileUpdates []model.ReleaseFileUpdate) []string {
	paths := make([]string, 0, len(fileUpdates))
	for _, update := range fileUpdates {
		paths = append(paths, ReleaseGitPath(projectRoot, update.Path))
	}
	return paths
}

func ReleaseGitCommand(dir string, args ...string) model.ReleaseCommandSpec {
	return model.ReleaseCommandSpec{
		Dir:  dir,
		Name: "git",
		Args: args,
	}
}

func ReleaseGitPath(projectRoot, path string) string {
	relative, err := filepath.Rel(projectRoot, path)
	if err != nil {
		return path
	}
	// git pathspecs use forward slashes on every OS.
	return filepath.ToSlash(filepath.Clean(relative))
}
