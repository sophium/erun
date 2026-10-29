package deploytarget

import (
	"github.com/sophium/erun/erun-common/base/seam/buildtarget"
)

type BuildDeployStore interface {
	DeployStore
	buildtarget.DockerStore
}
