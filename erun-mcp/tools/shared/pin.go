package shared

import (
	"github.com/sophium/erun/erun-common/base/model"
)

// PinOutput is the resolved plan, so a caller sees exactly which references
// moved rather than having to diff the tree afterwards.
type PinOutput struct {
	Plan    model.PinPlan `json:"plan"`
	Changed int           `json:"changed"`
	Applied bool          `json:"applied"`
}
