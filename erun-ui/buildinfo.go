package main

import (
	"os/exec"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

var (
	buildVersion = "dev"
	buildCommit  = ""
	buildDate    = ""
)

// buildSkillsSource and buildAgentsSource are the build checkout's
// erun-skills/skills and erun-skills/agents, stamped by the build scripts, so a
// bundle copied out of its checkout still installs the orchestrator's skills and agents.
var (
	buildSkillsSource = ""
	buildAgentsSource = ""
)

func currentBuildInfo() model.BuildInfo {
	return core.NormalizeBuildInfo(model.BuildInfo{
		Version: buildVersion,
		Commit:  buildCommit,
		Date:    buildDate,
	})
}

func resolveCurrentBuildInfo(resolveCLIPath func() string) model.BuildInfo {
	fallback := currentBuildInfo()
	if resolveCLIPath == nil {
		return fallback
	}

	cliPath := strings.TrimSpace(resolveCLIPath())
	if cliPath == "" {
		return fallback
	}

	versionCmd := exec.Command(cliPath, "version", "--no-registry")
	core.HideConsoleWindow(versionCmd)
	output, err := versionCmd.Output()
	if err != nil {
		return fallback
	}

	info, ok := parseVersionOutput(string(output))
	if !ok {
		return fallback
	}
	return core.NormalizeBuildInfo(info)
}

func parseVersionOutput(output string) (model.BuildInfo, bool) {
	lines := strings.Split(output, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if info, ok := parseVersionLine(lines[index]); ok {
			return info, true
		}
	}
	return model.BuildInfo{}, false
}

func parseVersionLine(line string) (model.BuildInfo, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "erun ") {
		return model.BuildInfo{}, false
	}

	rest := strings.TrimSpace(strings.TrimPrefix(line, "erun "))
	if rest == "" {
		return model.BuildInfo{}, false
	}

	version, details, _ := strings.Cut(rest, " ")
	info := model.BuildInfo{Version: strings.TrimSpace(version)}
	details = strings.TrimSpace(details)
	if strings.HasPrefix(details, "(") && strings.HasSuffix(details, ")") {
		details = strings.TrimSuffix(strings.TrimPrefix(details, "("), ")")
		commit, date, hasDate := strings.Cut(details, " built ")
		info.Commit = strings.TrimSpace(commit)
		if hasDate {
			info.Date = strings.TrimSpace(date)
		}
	}
	return info, info.Version != ""
}
