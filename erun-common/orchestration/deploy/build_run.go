package deploy

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
)

func DeployedVersionForSpecs(specs []helmdeploy.DeploySpec) string {
	version := ""
	for _, spec := range specs {
		current := strings.TrimSpace(spec.Deploy.Version)
		if current == "" {
			return ""
		}
		if version == "" {
			version = current
			continue
		}
		if current != version {
			return ""
		}
	}
	return version
}
