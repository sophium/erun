package core

import (
	"regexp"
)

// BuildkitStepOutputPattern matches one line a step printed, e.g.
//
//	#6 31.56 curl: (28) Connection timed out after 30002 milliseconds
//
// The elapsed-time field is what distinguishes a step's own output from
// BuildKit's framing around it.
var BuildkitStepOutputPattern = regexp.MustCompile(`^#(\d+) (\d+\.\d+) (.*)$`)
