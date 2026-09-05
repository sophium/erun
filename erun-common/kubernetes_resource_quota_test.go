package eruncommon

import (
	"strings"
	"testing"
)

// TestNamespaceResourceQuotaManifestDefaultRequestIsFixedNotTheCap pins #1076:
// the LimitRange's `default` (limit) tracks the namespace cap, but
// `defaultRequest` must stay a small fixed value regardless of the cap. Before
// the fix both fields echoed quota.CPU/Memory, so an unsized container's
// inherited request equalled the whole namespace allowance.
func TestNamespaceResourceQuotaManifestDefaultRequestIsFixedNotTheCap(t *testing.T) {
	quota := NamespaceResourceQuota{CPU: "8", Memory: "17832Mi", Storage: "72Gi"}
	manifest := namespaceResourceQuotaManifest("team-dev", quota)

	limitRange := manifest[strings.Index(manifest, "kind: LimitRange"):]

	defaultBlock := limitRange[strings.Index(limitRange, "default:"):strings.Index(limitRange, "defaultRequest:")]
	if !strings.Contains(defaultBlock, `cpu: "8"`) {
		t.Fatalf("default cpu limit should equal the namespace cap, got: %s", defaultBlock)
	}
	if !strings.Contains(defaultBlock, `memory: "17832Mi"`) {
		t.Fatalf("default memory limit should equal the namespace cap, got: %s", defaultBlock)
	}

	requestBlock := limitRange[strings.Index(limitRange, "defaultRequest:"):]
	if !strings.Contains(requestBlock, `cpu: "`+DefaultLimitRangeDefaultRequestCPU+`"`) {
		t.Fatalf("defaultRequest cpu should be the fixed constant %q, not the cap, got: %s", DefaultLimitRangeDefaultRequestCPU, requestBlock)
	}
	if !strings.Contains(requestBlock, `memory: "`+DefaultLimitRangeDefaultRequestMemory+`"`) {
		t.Fatalf("defaultRequest memory should be the fixed constant %q, not the cap, got: %s", DefaultLimitRangeDefaultRequestMemory, requestBlock)
	}
	if strings.Contains(requestBlock, `cpu: "8"`) {
		t.Fatalf("defaultRequest cpu must not equal the namespace cap: %s", requestBlock)
	}
	if strings.Contains(requestBlock, `memory: "17832Mi"`) {
		t.Fatalf("defaultRequest memory must not equal the namespace cap: %s", requestBlock)
	}
}

// TestNamespaceResourceQuotaManifestDefaultRequestIndependentOfCapSize
// changes the cap and asserts the fixed defaultRequest constants do not move
// with it — the property that makes an unsized container's reservation stay
// small however large an operator configures the namespace cap to be.
func TestNamespaceResourceQuotaManifestDefaultRequestIndependentOfCapSize(t *testing.T) {
	small := namespaceResourceQuotaManifest("team-dev", NamespaceResourceQuota{CPU: "2", Memory: "4096Mi", Storage: "20Gi"})
	large := namespaceResourceQuotaManifest("team-prod", NamespaceResourceQuota{CPU: "32", Memory: "65536Mi", Storage: "200Gi"})

	for _, manifest := range []string{small, large} {
		requestBlock := manifest[strings.Index(manifest, "defaultRequest:"):]
		if !strings.Contains(requestBlock, `cpu: "`+DefaultLimitRangeDefaultRequestCPU+`"`) {
			t.Fatalf("defaultRequest cpu should stay fixed at %q regardless of cap, got: %s", DefaultLimitRangeDefaultRequestCPU, requestBlock)
		}
		if !strings.Contains(requestBlock, `memory: "`+DefaultLimitRangeDefaultRequestMemory+`"`) {
			t.Fatalf("defaultRequest memory should stay fixed at %q regardless of cap, got: %s", DefaultLimitRangeDefaultRequestMemory, requestBlock)
		}
	}
}
