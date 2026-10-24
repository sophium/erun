package kube

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
)

func TestNamespaceResourceQuotaManifestDefaultRequestIsFixedNotTheCap(t *testing.T) {
	quota := config.NamespaceResourceQuota{CPU: "8", Memory: "17832Mi", Storage: "72Gi"}
	manifest := namespaceResourceQuotaManifest("team-dev", quota)

	limitRange := fromMarker(t, manifest, "kind: LimitRange")

	defaultBlock := limitRange[strings.Index(limitRange, "default:"):strings.Index(limitRange, "defaultRequest:")]
	if !strings.Contains(defaultBlock, `cpu: "8"`) {
		t.Fatalf("default cpu limit should equal the namespace cap, got: %s", defaultBlock)
	}
	if !strings.Contains(defaultBlock, `memory: "17832Mi"`) {
		t.Fatalf("default memory limit should equal the namespace cap, got: %s", defaultBlock)
	}

	requestBlock := fromMarker(t, limitRange, "defaultRequest:")
	if !strings.Contains(requestBlock, `cpu: "`+config.DefaultLimitRangeDefaultRequestCPU+`"`) {
		t.Fatalf("defaultRequest cpu should be the fixed constant %q, not the cap, got: %s", config.DefaultLimitRangeDefaultRequestCPU, requestBlock)
	}
	if !strings.Contains(requestBlock, `memory: "`+config.DefaultLimitRangeDefaultRequestMemory+`"`) {
		t.Fatalf("defaultRequest memory should be the fixed constant %q, not the cap, got: %s", config.DefaultLimitRangeDefaultRequestMemory, requestBlock)
	}
	if strings.Contains(requestBlock, `cpu: "8"`) {
		t.Fatalf("defaultRequest cpu must not equal the namespace cap: %s", requestBlock)
	}
	if strings.Contains(requestBlock, `memory: "17832Mi"`) {
		t.Fatalf("defaultRequest memory must not equal the namespace cap: %s", requestBlock)
	}
}

func TestNamespaceResourceQuotaManifestDefaultRequestIndependentOfCapSize(t *testing.T) {
	small := namespaceResourceQuotaManifest("team-dev", config.NamespaceResourceQuota{CPU: "2", Memory: "4096Mi", Storage: "20Gi"})
	large := namespaceResourceQuotaManifest("team-prod", config.NamespaceResourceQuota{CPU: "32", Memory: "65536Mi", Storage: "200Gi"})

	for _, manifest := range []string{small, large} {
		requestBlock := fromMarker(t, manifest, "defaultRequest:")
		if !strings.Contains(requestBlock, `cpu: "`+config.DefaultLimitRangeDefaultRequestCPU+`"`) {
			t.Fatalf("defaultRequest cpu should stay fixed at %q regardless of cap, got: %s", config.DefaultLimitRangeDefaultRequestCPU, requestBlock)
		}
		if !strings.Contains(requestBlock, `memory: "`+config.DefaultLimitRangeDefaultRequestMemory+`"`) {
			t.Fatalf("defaultRequest memory should stay fixed at %q regardless of cap, got: %s", config.DefaultLimitRangeDefaultRequestMemory, requestBlock)
		}
	}
}

func fromMarker(t *testing.T, s, marker string) string {
	t.Helper()
	i := strings.Index(s, marker)
	if i < 0 {
		t.Fatalf("missing %q in: %s", marker, s)
	}
	return s[i:]
}
