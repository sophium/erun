package helmrun

import (
	"io"

	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
)

// HelmDeployParamsFor carries the whole spec so the executed command matches the traced one.
func HelmDeployParamsFor(d helmdeploy.HelmDeploySpec, stdout, stderr io.Writer) helmdeploy.HelmDeployParams {
	return helmdeploy.HelmDeployParams{HelmDeploySpec: d, Stdout: stdout, Stderr: stderr}
}
