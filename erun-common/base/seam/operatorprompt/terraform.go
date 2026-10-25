package operatorprompt

import (
	"github.com/sophium/erun/erun-common/base/core"
)

// TerraformConfirmFunc makes the operator restate the environment name before
// apply/destroy; it is never called for plan or dry-run.
type TerraformConfirmFunc func(ctx core.Context, environment string) error
