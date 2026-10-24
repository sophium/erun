package releaseclaim

import (
	"io"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

// ReleasePushStderrWriter: io.MultiWriter panics on a nil writer, so a nil sink uses the capture alone.
func ReleasePushStderrWriter(ctx core.Context, capture *strings.Builder) io.Writer {
	if ctx.Stderr == nil {
		return capture
	}
	return io.MultiWriter(ctx.Stderr, capture)
}
