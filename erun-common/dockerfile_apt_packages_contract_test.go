package eruncommon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// aptInstallTokens returns the package names named by one physical line of an
// `apt-get install` command, and whether that command continues onto the next
// line. Flags (`-y`, `--no-install-recommends`) and `VAR=value` assignments are
// skipped; a shell metacharacter ends the command for good, so the `&& \` that
// typically follows the package list does not drag the next command's words in.
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

// dockerfileStage is one FROM in a Dockerfile: its base image reference, the
// raw lines of its body, and every package it installs through apt-get install.
// The lines are what a caller asking "which stage does X?" reads; the package
// set is what the apt contract reads.
type dockerfileStage struct {
	base     string
	lines    []string
	packages []string
}

// dockerfileStages splits a Dockerfile into one entry per FROM, in file order,
// carrying the base image reference, the stage's own lines, and the packages
// that stage apt-get installs. Comment lines are skipped when reading packages
// so prose in the file cannot be mistaken for a package list, which is the
// failure mode a bare substring scan has; they stay in the stage's lines, which
// are the stage's text as written.
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

// stageBasedOn returns the single stage whose FROM names an image containing
// nameFragment, failing the test when there is not exactly one.
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

// erunDevopsDesktopRuntimePackages are the packages the erun-devops image needs
// for the Wails/webkit desktop toolchain and the dtach-backed MCP attach
// suite. They are the set the final stage used to apt-get install for itself
// and now inherits from the erun-ubuntu base instead.
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

// TestErunDevopsFinalImageStillInstallsItsDesktopRuntimePackages locks the
// correctness half of moving the Wails/webkit set out of the erun-devops final
// stage and into the erun-ubuntu base image. That move is a pure build-cost
// deduplication — the set used to be apt-get installed twice per build — and
// it is only sound while the base really provides every package the final
// stage stopped installing. Nothing else checks that: the two lists live in
// different Dockerfiles, and dropping one from erun-ubuntu's list would not
// fail any render, lint, or unit test. It would ship an image missing a
// webkit2gtk or GTK dev header, which surfaces far away as a CGO compile
// failure in the in-pod contribute-mode workflow or a missing shared library
// in a Playwright run — the failure the move was explicitly not allowed to
// trade for speed. This test resolves the union the final image actually ends
// up with, across both files, and fails in the same run that breaks it.
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

// TestErunDevopsFinalStageDoesNotReinstallWhatTheBaseProvides is the cost half
// of the same move: it fails when the final stage starts apt-get installing a
// package it already inherits, which is how the duplication this change
// removed would come back. It is deliberately separate from the correctness
// test above — re-adding one of these to the final stage still produces a
// correct image, just a slower build — so a reviewer sees "the dedup
// regressed" rather than "the image is broken".
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
