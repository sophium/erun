package model

import (
	"slices"
	"strings"
)

type RuntimeRegistryVersions struct {
	Image          string
	Tags           []string
	LatestStable   string
	LatestSnapshot string
}

func (versions RuntimeRegistryVersions) HasVersion(version string) bool {
	version = strings.TrimSpace(version)
	if version == "" || len(versions.Tags) == 0 {
		return false
	}
	return slices.Contains(versions.Tags, version)
}
