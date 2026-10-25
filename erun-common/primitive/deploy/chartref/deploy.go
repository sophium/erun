package chartref

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
)

func ResolvedChartVersion(d helmdeploy.HelmDeploySpec) string {
	if version := strings.TrimSpace(d.ChartVersion); version != "" {
		return version
	}
	return strings.TrimSpace(d.Version)
}

// ChartName is a reference's last path segment, as helm reads it.
func ChartName(reference string) string {
	return reference[strings.LastIndex(reference, "/")+1:]
}

// SplitChartReference separates a chart reference into repository and optional version.
func SplitChartReference(raw string) (reference, version string) {
	return SplitChartReferenceVersion(raw)
}

// ChartNameFromReference is ChartName for callers outside this package.
func ChartNameFromReference(reference string) string {
	return ChartName(reference)
}

// SplitChartReferenceVersion inspects only the last path segment, so a registry port is not a version,
// and adds oci:// when absent.
func SplitChartReferenceVersion(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "oci://" + raw
	}
	cut := strings.LastIndex(raw, "/")
	if cut < 0 {
		return raw, ""
	}
	last := raw[cut+1:]
	sep := strings.LastIndex(last, ":")
	if sep <= 0 {
		return raw, ""
	}
	return raw[:cut+1] + last[:sep], last[sep+1:]
}

func SpecDeploysRuntimeChart(spec helmdeploy.DeploySpec) bool {
	tenant := strings.TrimSpace(spec.Target.Tenant)
	return tenant != "" && strings.TrimSpace(spec.Deploy.ReleaseName) == runtimetarget.RuntimeReleaseName(tenant)
}
