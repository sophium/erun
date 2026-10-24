package helmrun

import (
	"io"

	"github.com/sophium/erun/erun-common/base/seam"
)

// HelmDeployParamsFor carries the whole spec so the executed command matches the traced one.
func HelmDeployParamsFor(d seam.HelmDeploySpec, stdout, stderr io.Writer) seam.HelmDeployParams {
	return seam.HelmDeployParams{HelmDeploySpec: d, Stdout: stdout, Stderr: stderr}
}
