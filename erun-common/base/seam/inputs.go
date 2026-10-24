package seam

import (
	"io"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

// RuntimeInputUploadRunner runs a /bin/sh script in the runtime pod with stdin streamed to it.
type RuntimeInputUploadRunner func(req config.ShellLaunchParams, script string, stdin io.Reader) (model.RemoteCommandResult, error)
