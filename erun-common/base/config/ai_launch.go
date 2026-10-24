package config

import (
	"regexp"
)

// ClaudeModelTokenPattern guards a persisted model name so it can only reach the
// launch script as a plain argv token, never shell or flag syntax. The first
// character excludes '-': a value such as `--dangerously-skip-permissions` is
// made of allowed characters and would otherwise reach `--model` as a flag.
var ClaudeModelTokenPattern = regexp.MustCompile(`^[A-Za-z0-9._:/][A-Za-z0-9._:/-]*$`)
