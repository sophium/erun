package eruncommon

import (
	"fmt"
	"strings"
)

// resourceQuotaName and limitRangeName are deterministic per namespace, so a
// re-applied deploy updates the same objects instead of accumulating copies.
const (
	resourceQuotaName = "erun-quota"
	limitRangeName    = "erun-limits"
)

// namespaceResourceQuotaManifest renders the ResourceQuota + LimitRange YAML
// for one namespace's cap. The LimitRange supplies a default/defaultRequest so
// a pod that declares no resources of its own (nothing in this namespace is
// assumed to) still gets sane values instead of the ResourceQuota rejecting it
// outright for omitting requests. The default equals the cap itself divided
// across a small fixed pod budget, keeping this generic rather than baking in
// the runtime chart's own values.
func namespaceResourceQuotaManifest(namespace string, quota NamespaceResourceQuota) string {
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
		quota.CPU, quota.Memory,
	)
}

// TraceApplyKubernetesResourceQuota records the dry-run plan for the namespace
// cap, mirroring TraceEnsureKubernetesNamespace. A zero quota traces nothing —
// deploy applies no ResourceQuota/LimitRange when the env has no cap configured.
func TraceApplyKubernetesResourceQuota(ctx Context, contextName, namespace string, quota NamespaceResourceQuota) {
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

// EnsureKubernetesResourceQuota applies (creates or updates) the namespace's
// ResourceQuota + LimitRange from quota. A zero quota is a no-op, so an
// environment with no cap configured deploys exactly as it did before this
// existed.
func EnsureKubernetesResourceQuota(contextName, namespace string, quota NamespaceResourceQuota) error {
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

	cmd := Command("kubectl", args...)
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
