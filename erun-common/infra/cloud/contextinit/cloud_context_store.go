package contextinit

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud/cloudcontext"
)

func validCloudContextInstanceType(value string) bool {
	for _, option := range cloudcontext.CloudContextInstanceTypes() {
		if value == option {
			return true
		}
	}
	return false
}

func validCloudContextDiskSize(value int) bool {
	for _, option := range cloudcontext.CloudContextDiskSizesGB() {
		if value == option {
			return true
		}
	}
	return false
}

func validCloudContextRegion(value string) bool {
	for _, option := range cloudcontext.CloudContextRegions() {
		if value == option {
			return true
		}
	}
	return false
}

func generatedCloudContextName(provider model.CloudProviderConfig, region string, existingContexts []model.CloudContextConfig) string {
	parts := make([]string, 0, 2)
	switch {
	case provider.AccountID != "":
		parts = append(parts, provider.AccountID)
	case provider.Username != "":
		parts = append(parts, provider.Username)
	default:
		parts = append(parts, provider.Alias)
	}
	if region != "" {
		parts = append(parts, region)
	}
	tail := sanitizeCloudContextName(strings.Join(parts, "-"))
	return nextCloudContextName(tail, existingContexts)
}

func nextCloudContextName(tail string, existingContexts []model.CloudContextConfig) string {
	tail = sanitizeCloudContextName(tail)
	if tail == "" {
		tail = "context"
	}
	prefix := "erun-"
	suffix := "-" + tail
	next := 1
	for _, context := range existingContexts {
		for _, name := range []string{context.Name, context.KubernetesContext} {
			name = strings.TrimSpace(name)
			if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
				continue
			}
			counter := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
			if len(counter) != 3 {
				continue
			}
			value, err := strconv.Atoi(counter)
			if err == nil && value >= next {
				next = value + 1
			}
		}
	}
	return fmt.Sprintf("erun-%03d-%s", next, tail)
}

func sanitizeCloudContextName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if ok {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}
