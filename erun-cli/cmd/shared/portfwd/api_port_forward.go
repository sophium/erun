package portfwd

import (
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

type APIForwarder func(core.Context, config.OpenResult) error
