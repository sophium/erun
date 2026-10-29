//go:build !linux

package podruntime

import (
	"runtime"
)

func defaultDindEntrypointHost() (dindEntrypointHost, error) {
	return dindEntrypointHost{}, dindEntrypointUnsupportedError(runtime.GOOS)
}
