package envscope

import (
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

// OpenResolver resolves an env's runtime target from tenant/environment scope.
type OpenResolver func(model.OpenParams) (config.OpenResult, error)
