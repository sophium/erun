package core

import (
	"regexp"
)

// BuildkitStepOutputPattern matches a step output line such as
//
//	#6 31.56 curl: (28) Connection timed out after 30002 milliseconds
var BuildkitStepOutputPattern = regexp.MustCompile(`^#(\d+) (\d+\.\d+) (.*)$`)
