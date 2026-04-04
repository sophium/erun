package eruncommon

import "fmt"

type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

func NormalizeBuildInfo(info BuildInfo) BuildInfo {
	if info.Version == "" {
		info.Version = "dev"
	}
	return info
}

func FormatVersionLine(info BuildInfo) string {
	info = NormalizeBuildInfo(info)

	line := fmt.Sprintf("erun %s", info.Version)
	if info.Commit == "" {
		return line
	}

	line += fmt.Sprintf(" (%s", info.Commit)
	if info.Date != "" {
		line += fmt.Sprintf(" built %s", info.Date)
	}
	line += ")"
	return line
}
