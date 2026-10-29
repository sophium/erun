package chartref

import (
	"strings"
)

func IsOCIChartReference(chartPath string) bool {
	return strings.HasPrefix(strings.TrimSpace(chartPath), "oci://")
}
