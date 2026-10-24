package registry

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

func IsGHCRRegistry(registry string) bool {
	registry = strings.ToLower(strings.TrimSpace(registry))
	return registry == "ghcr.io" || strings.HasPrefix(registry, "ghcr.io/")
}

func CaptureGHCommand(args ...string) (string, error) {
	cmd := core.Command("gh", args...)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}
