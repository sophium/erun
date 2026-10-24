package deploy

import (
	"bytes"

	"github.com/sophium/erun/erun-common/base/core"
)

func traceCapturingContext() (core.Context, *bytes.Buffer) {
	var trace bytes.Buffer
	logger := core.NewLogger(core.VerbosityInfo).WithTraceSink(&trace)
	return core.Context{Logger: logger}, &trace
}
