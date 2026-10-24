package orchestrator

import (
	"fmt"

	configpkg "github.com/sophium/erun/erun-common/base/config"
)

// ValidateClaudeModelID reports whether id is a model id a launch can pass:
// letters, digits and . _ : / -, never starting with -.
func ValidateClaudeModelID(id string) error {
	if !configpkg.ClaudeModelTokenPattern.MatchString(id) {
		return fmt.Errorf("invalid model %q: a model id is letters, digits and . _ : / -, and cannot start with -", id)
	}
	return nil
}
