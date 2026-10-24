package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// aptInstallTokens returns the packages on one `apt-get install` line and whether it continues.
// A shell metacharacter ends the command, so `&& \` does not pull in the next command.
func aptInstallTokens(line string) (packages []string, continues bool) {
	line = strings.TrimRight(line, " \t")
	if strings.HasSuffix(line, "\\") {
		continues = true
		line = strings.TrimSuffix(line, "\\")
	}
	for _, token := range strings.Fields(line) {
		switch token {
		case "&&", "||", ";", "|":
			return packages, false
		}
		if strings.HasPrefix(token, "-") || strings.Contains(token, "=") {
			continue
		}
		packages = append(packages, token)
	}
	return packages, continues
}

type dockerfileStage struct {
	base     string
	lines    []string
	packages []string
}

// dockerfileStages splits a Dockerfile per FROM; comment lines are skipped when reading packages.
func dockerfileStages(text string) []dockerfileStage {
	var stages []dockerfileStage
	inInstall := false
	for _, raw := range strings.Split(text, "\n") {
		if match := dockerfileFromRefPattern.FindStringSubmatch(raw); match != nil {
			stages = append(stages, dockerfileStage{base: match[1]})
			inInstall = false
			continue
		}
		if len(stages) == 0 {
			continue
		}
		last := len(stages) - 1
		stages[last].lines = append(stages[last].lines, raw)
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var rest string
		if inInstall {
			rest = line
		} else if idx := strings.Index(line, "apt-get install"); idx >= 0 {
			rest = line[idx+len("apt-get install"):]
		} else {
			continue
		}
		packages, continues := aptInstallTokens(rest)
		stages[last].packages = append(stages[last].packages, packages...)
		inInstall = continues
	}
	return stages
}

func stageBasedOn(t *testing.T, stages []dockerfileStage, dockerfile, nameFragment string) dockerfileStage {
	t.Helper()
	var found []dockerfileStage
	for _, stage := range stages {
		if strings.Contains(stage.base, nameFragment) {
			found = append(found, stage)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%s: want exactly one stage based on an image containing %q, found %d", dockerfile, nameFragment, len(found))
	}
	return found[0]
}

func aptPackageSet(stages ...dockerfileStage) map[string]struct{} {
	set := make(map[string]struct{})
	for _, stage := range stages {
		for _, pkg := range stage.packages {
			set[pkg] = struct{}{}
		}
	}
	return set
}

// erunDevopsDesktopRuntimePackages are inherited by erun-devops from the erun-ubuntu base.
var erunDevopsDesktopRuntimePackages = []string{
	"dtach",
	"build-essential",
	"pkg-config",
	"libgtk-3-dev",
	"libwebkit2gtk-4.1-dev",
	"libsoup-3.0-dev",
}

func erunDevopsStageDockerfiles(t *testing.T) (baseStage, finalStage dockerfileStage) {
	t.Helper()
	root := repoRootForDockerignoreTest(t)
	basePath := filepath.Join(root, "erun-devops", "docker", "erun-ubuntu", "Dockerfile")
	finalPath := filepath.Join(root, "erun-devops", "docker", "erun-devops", "Dockerfile")
	baseData, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatalf("read %s: %v", basePath, err)
	}
	finalData, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("read %s: %v", finalPath, err)
	}
	return stageBasedOn(t, dockerfileStages(string(baseData)), basePath, "ubuntu"),
		stageBasedOn(t, dockerfileStages(string(finalData)), finalPath, "erun-ubuntu")
}

// The package lists live in different Dockerfiles; nothing else catches a dropped one.
func TestErunDevopsFinalImageStillInstallsItsDesktopRuntimePackages(t *testing.T) {
	baseStage, finalStage := erunDevopsStageDockerfiles(t)
	available := aptPackageSet(baseStage, finalStage)
	for _, pkg := range erunDevopsDesktopRuntimePackages {
		if _, ok := available[pkg]; ok {
			continue
		}
		t.Errorf("the erun-devops final image would lose %q: the erun-ubuntu base (%s) no longer apt-get installs it and the final stage does not either, so a Wails/webkit CGO build or a Playwright run inside the image fails on a missing library long after this change", pkg, baseStage.base)
	}
}

// Separate from the correctness test: a reinstall is a slower build, not a broken image.
func TestErunDevopsFinalStageDoesNotReinstallWhatTheBaseProvides(t *testing.T) {
	baseStage, finalStage := erunDevopsStageDockerfiles(t)
	base := aptPackageSet(baseStage)
	for _, pkg := range erunDevopsDesktopRuntimePackages {
		if _, inherited := base[pkg]; !inherited {
			continue // the correctness test above covers a package the base does not provide
		}
		for _, installed := range finalStage.packages {
			if installed == pkg {
				t.Errorf("the erun-devops final stage apt-get installs %q, which the erun-ubuntu base (%s) already provides — that re-adds the per-build unpack/configure cost this move removed", pkg, baseStage.base)
			}
		}
	}
}
