package core

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
)

// ParseOutputMode validates a raw --output value; empty means text.
func ParseOutputMode(raw string) (model.OutputMode, error) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", string(model.OutputText):
		return model.OutputText, nil
	case string(model.OutputJSON):
		return model.OutputJSON, nil
	default:
		return model.OutputText, fmt.Errorf("invalid --output value %q: expected %q or %q", raw, model.OutputText, model.OutputJSON)
	}
}
