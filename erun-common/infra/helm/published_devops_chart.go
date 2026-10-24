package helm

import (
	"strings"
)

// PublishedChartRepoPath is the path segment every published chart lives under,
// keeping the chart's tag space separate from the image repository of the same
// name (<registry>/erun-devops), which holds the image tags.
const PublishedChartRepoPath = "charts"

// PublishedDevopsChartOCIRepo is the OCI repository the release flow pushes
// the canonical runtime chart to. The "/charts" suffix keeps the chart's tag
// space separate from the image repository of the same name
// (<registry>/erun-devops), which holds the runtime image tags.
func PublishedDevopsChartOCIRepo(containerRegistry string) string {
	return "oci://" + strings.TrimSpace(containerRegistry) + "/" + PublishedChartRepoPath
}
