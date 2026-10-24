package kube

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// Deterministic names make a re-applied deploy update the same objects.
const (
	resourceQuotaName = "erun-quota"
	limitRangeName    = "erun-limits"
)

// The LimitRange defaults unsized pods so the quota does not reject them. defaultRequest
// is a small fixed value: a request equal to the cap would make the cap a minimum node size.
func namespaceResourceQuotaManifest(namespace string, quota model.NamespaceResourceQuota) string {
	return fmt.Sprintf(`apiVersion: v1
kind: ResourceQuota
metadata:
  name: %s
  namespace: %s
spec:
  hard:
    limits.cpu: %q
    limits.memory: %q
    requests.storage: %q
---
apiVersion: v1
kind: LimitRange
metadata:
  name: %s
  namespace: %s
spec:
  limits:
    - type: Container
      default:
        cpu: %q
        memory: %q
      defaultRequest:
        cpu: %q
        memory: %q
`,
		resourceQuotaName, namespace,
		quota.CPU, quota.Memory, quota.Storage,
		limitRangeName, namespace,
		quota.CPU, quota.Memory,
		config.DefaultLimitRangeDefaultRequestCPU, config.DefaultLimitRangeDefaultRequestMemory,
	)
}

// TraceApplyKubernetesResourceQuota traces the namespace cap; a zero quota traces nothing.
func TraceApplyKubernetesResourceQuota(ctx core.Context, contextName, namespace string, quota model.NamespaceResourceQuota) {
	if quota.IsZero() || strings.TrimSpace(namespace) == "" {
		return
	}
	args := []string{}
	if contextName = strings.TrimSpace(contextName); contextName != "" {
		args = append(args, "--context", contextName)
	}
	args = append(args, "apply", "-n", namespace, "-f", "-")
	ctx.TraceCommand("", "kubectl", args...)
}

// EnsureKubernetesResourceQuota applies the namespace's ResourceQuota and LimitRange; a zero quota is a no-op.
func EnsureKubernetesResourceQuota(contextName, namespace string, quota model.NamespaceResourceQuota) error {
	if quota.IsZero() {
		return nil
	}
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return fmt.Errorf("namespace is required to apply a resource quota")
	}
	contextName = strings.TrimSpace(contextName)
	args := []string{}
	if contextName != "" {
		args = append(args, "--context", contextName)
	}
	args = append(args, "apply", "-n", namespace, "-f", "-")

	cmd := core.Command("kubectl", args...)
	cmd.Stdin = strings.NewReader(namespaceResourceQuotaManifest(namespace, quota))
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return fmt.Errorf("failed to apply resource quota in namespace %q context %q: %w", namespace, contextName, err)
		}
		return fmt.Errorf("failed to apply resource quota in namespace %q context %q: %w: %s", namespace, contextName, err, message)
	}
	return nil
}
