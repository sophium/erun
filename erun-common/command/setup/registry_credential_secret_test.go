package setup

import (
	"io"

	"github.com/sophium/erun/erun-common/base/core"
)

func testTraceContext(dryRun bool) core.Context {
	return core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, io.Discard, io.Discard), DryRun: dryRun}
}
