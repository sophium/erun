package main

import (
	"os/exec"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

var (
	buildVersion = "dev"
	buildCommit  = ""
	buildDate    = ""
)

func currentBuildInfo() eruncommon.BuildInfo {
	return eruncommon.NormalizeBuildInfo(eruncommon.BuildInfo{
		Version: buildVersion,
		Commit:  buildCommit,
		Date:    buildDate,
	})
}

func resolveCurrentBuildInfo(resolveCLIPath func() string) eruncommon.BuildInfo {
	fallback := currentBuildInfo()
	if resolveCLIPath == nil {
		return fallback
	}

	cliPath := strings.TrimSpace(resolveCLIPath())
	if cliPath == "" {
		return fallback
	}

	output, err := exec.Command(cliPath, "version", "--no-registry").Output()
	if err != nil {
		return fallback
	}

	info, ok := parseVersionOutput(string(output))
	if !ok {
		return fallback
	}
	return eruncommon.NormalizeBuildInfo(info)
}

func parseVersionOutput(output string) (eruncommon.BuildInfo, bool) {
	lines := strings.Split(output, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if info, ok := parseVersionLine(lines[index]); ok {
			return info, true
		}
	}
	return eruncommon.BuildInfo{}, false
}

func parseVersionLine(line string) (eruncommon.BuildInfo, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "erun ") {
		return eruncommon.BuildInfo{}, false
	}

	rest := strings.TrimSpace(strings.TrimPrefix(line, "erun "))
	if rest == "" {
		return eruncommon.BuildInfo{}, false
	}

	version, details, _ := strings.Cut(rest, " ")
	info := eruncommon.BuildInfo{Version: strings.TrimSpace(version)}
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
