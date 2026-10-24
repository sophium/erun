package deploy

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
)

func TestRuntimeImageOlderThanTheStockChartIsRefused(t *testing.T) {
	err := guardRuntimeImageNotOlderThanChart("ghcr.io/sophium/erun-devops:1.0.296", config.DevopsComponentName, "1.0.307")
	if err == nil || !strings.Contains(err.Error(), "older than the erun-devops 1.0.307 chart") || !strings.Contains(err.Error(), "--runtime-chart erun-devops:1.0.296") {
		t.Fatalf("an older stock image under the stock chart should be refused with both remedies, got %v", err)
	}
	for _, ok := range []struct{ image, chart, version string }{
		{"ghcr.io/sophium/erun-devops:1.0.307", config.DevopsComponentName, "1.0.307-snapshot"},
		{"ghcr.io/sophium/erun-devops:1.0.307-snapshot-20261022181909-arm64", config.DevopsComponentName, "1.0.307"},
		{"ghcr.io/sophium/erun-devops:1.0.308", config.DevopsComponentName, "1.0.307"},
		{"ghcr.io/sophium/frs-devops:1.0.1", config.DevopsComponentName, "1.0.307"},
		{"ghcr.io/sophium/erun-devops:1.0.296", "frs-devops", "1.0.307"},
		{"ghcr.io/sophium/erun-devops:fp-c93a0b4ddb9869f3-arm64", config.DevopsComponentName, "1.0.307"},
		{"ghcr.io/sophium/erun-devops:1.0.296", config.DevopsComponentName, ""},
	} {
		if err := guardRuntimeImageNotOlderThanChart(ok.image, ok.chart, ok.version); err != nil {
			t.Fatalf("%s under %s %s should proceed, got %v", ok.image, ok.chart, ok.version, err)
		}
	}
}
