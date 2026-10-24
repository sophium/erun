package helm

import (
	"strings"
)

// PublishedChartRepoPath separates chart tags from the same-named image repository.
const PublishedChartRepoPath = "charts"

// PublishedDevopsChartOCIRepo is where the release flow pushes the runtime chart.
func PublishedDevopsChartOCIRepo(containerRegistry string) string {
	return "oci://" + strings.TrimSpace(containerRegistry) + "/" + PublishedChartRepoPath
}
