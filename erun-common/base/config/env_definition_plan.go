package config

import (
	"strings"
)

func ValueOrUnset(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unset"
	}
	return value
}
