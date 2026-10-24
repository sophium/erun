package cloud

import (
	"strings"
)

func cloudContextRegionFromName(name string) string {
	name = strings.TrimSpace(name)
	for _, region := range CloudContextRegions() {
		if strings.HasSuffix(name, "-"+region) || name == region {
			return region
		}
	}
	return ""
}
