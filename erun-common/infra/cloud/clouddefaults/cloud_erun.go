package clouddefaults

import (
	"github.com/sophium/erun/erun-common/base/core"
)

func WriteERunLoginPrompt(ctx core.Context, message string) {
	if ctx.Stdout == nil {
		return
	}
	_, _ = ctx.Stdout.Write([]byte(message))
}
