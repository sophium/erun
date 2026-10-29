package gitcmd

import (
	"io"

	"github.com/sophium/erun/erun-common/base/core"
)

type (
	GitValueResolverFunc func(core.Context, string) (string, error)
	GitCommandRunnerFunc func(string, io.Writer, io.Writer, ...string) error
)
