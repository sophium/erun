package config

import (
	"regexp"
)

// ClaudeModelTokenPattern restricts a model name to a plain argv token; a leading '-' is refused so it cannot become a flag.
var ClaudeModelTokenPattern = regexp.MustCompile(`^[A-Za-z0-9._:/][A-Za-z0-9._:/-]*$`)
