package core

import (
	"fmt"

	"github.com/sophium/erun/erun-common/base/model"
)

func NormalizeBuildInfo(info model.BuildInfo) model.BuildInfo {
	if info.Version == "" {
		info.Version = "dev"
	}
	return info
}

func FormatVersionLine(info model.BuildInfo) string {
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
