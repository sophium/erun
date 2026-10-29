package desktop

import (
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

var CurrentHostOS = func() model.HostOS { return core.DetectHost().OS }
