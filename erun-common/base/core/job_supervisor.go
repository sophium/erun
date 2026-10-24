package core

import (
	"fmt"
	"strings"
)

func NormalizeEnvironmentJobSignal(signal string) (string, error) {
	value := strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(signal)), "SIG")))
	if value == "" {
		return "TERM", nil
	}
	switch value {
	case "TERM", "INT", "HUP", "KILL":
		return value, nil
	default:
		return "", fmt.Errorf("unsupported signal %q: expected TERM, INT, HUP, or KILL", signal)
	}
}

// FirstNonEmptyString returns the first non-empty value, or "" if all are.
func FirstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
