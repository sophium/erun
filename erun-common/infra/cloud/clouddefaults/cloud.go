package clouddefaults

import (
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
)

func NormalizeCloudBearerAudience(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return configpkg.CloudProviderBearerAudience
	}
	return value
}
