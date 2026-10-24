package seam

import (
	"github.com/sophium/erun/erun-common/base/config"
)

// ExposeStore resolves the target environment for RunExposeService.
type ExposeStore interface {
	LoadEnvConfig(string, string) (config.EnvConfig, string, error)
}
