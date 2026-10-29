package releasespec

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/shared/projectseam"
)

// erun-app.exe is load-bearing: `erun app`, Homebrew and Scoop launchers depend on the name.
var requiredScoopBinaries = []string{"erun.exe", "emcp.exe", "eapi.exe", "erun-app.exe"}

// Runs during release resolution, so a drifted Scoop manifest fails before any git mutation.
func validateStableScoopManifest(ctx core.Context, projectRoot string) error {
	scoopPath := filepath.Join(projectRoot, "bucket", "erun.json")
	if !projectseam.FileExists(scoopPath) {
		return nil
	}
	ctx.Trace("release: validating scoop manifest " + scoopPath)
	return checkScoopManifestInvariants(scoopPath)
}

// Reports every violation at once.
func checkScoopManifestInvariants(scoopPath string) error {
	data, err := os.ReadFile(scoopPath)
	if err != nil {
		return err
	}
	var manifest struct {
		Depends   []string `json:"depends"`
		Installer struct {
			Script []string `json:"script"`
		} `json:"installer"`
		Bin []string `json:"bin"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("scoop manifest %s: %w", scoopPath, err)
	}

	var problems []string
	if !slices.Contains(manifest.Depends, "mingw") {
		problems = append(problems, `depends must include "mingw" for the Wails CGO build`)
	}
	if len(manifest.Installer.Script) == 0 {
		problems = append(problems, "installer.script must not be empty")
	}
	script := strings.Join(manifest.Installer.Script, "\n")
	if !strings.Contains(script, "MinGW for the Wails CGO build") {
		problems = append(problems, "installer.script must state the MinGW/Wails CGO prerequisite")
	}
	if strings.Contains(script, "Fyne") {
		problems = append(problems, "installer.script must not reference the stale Fyne prerequisite")
	}
	for _, bin := range requiredScoopBinaries {
		if !slices.Contains(manifest.Bin, bin) {
			problems = append(problems, "bin must include "+bin)
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("scoop manifest %s is invalid: %s", scoopPath, strings.Join(problems, "; "))
	}
	return nil
}

func discoverStableReleasePackaging(projectRoot, version string) ([]model.ReleaseFileUpdate, *model.ReleasePackagingSyncSpec, error) {
	updates := make([]model.ReleaseFileUpdate, 0, 2)
	syncSpec := &model.ReleasePackagingSyncSpec{ProjectRoot: projectRoot, Version: version}

	formulaPath := filepath.Join(projectRoot, "Formula", "erun.rb")
	updates, err := appendReleasePackagingUpdate(updates, formulaPath, version, updateHomebrewFormulaReleaseVersion)
	if err != nil {
		return nil, nil, err
	}
	if projectseam.FileExists(formulaPath) {
		syncSpec.FormulaPath = formulaPath
	}

	scoopPath := filepath.Join(projectRoot, "bucket", "erun.json")
	updates, err = appendReleasePackagingUpdate(updates, scoopPath, version, updateScoopManifestReleaseVersion)
	if err != nil {
		return nil, nil, err
	}
	if projectseam.FileExists(scoopPath) {
		syncSpec.ScoopPath = scoopPath
	}

	marketplacePath := filepath.Join(projectRoot, ".claude-plugin", "marketplace.json")
	if projectseam.FileExists(marketplacePath) {
		syncSpec.MarketplacePath = marketplacePath
	}

	if syncSpec.FormulaPath == "" && syncSpec.ScoopPath == "" && syncSpec.MarketplacePath == "" {
		return updates, nil, nil
	}

	return updates, syncSpec, nil
}

func appendReleasePackagingUpdate(
	updates []model.ReleaseFileUpdate,
	path, version string,
	update func(string, string) (string, bool, error),
) ([]model.ReleaseFileUpdate, error) {
	content, changed, err := update(path, version)
	if err != nil {
		return nil, err
	}
	if changed {
		updates = append(updates, model.ReleaseFileUpdate{
			Path:    path,
			Content: content,
		})
	}
	return updates, nil
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
