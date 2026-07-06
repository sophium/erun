package eruncommon

import (
	"fmt"
	"strconv"
	"strings"
)

// powerDNSConfigDir holds the PowerDNS backend config and DB password, so the
// pdnsutil exec carries no connection flags and interpolates no secret.
const powerDNSConfigDir = "/etc/pdns-shared"

// powerDNSUpsertArgs is shared by the dry-run trace and the live exec so they stay
// identical. Tokens pass as direct argv (no `sh -c`), so the wildcard name needs no
// shell quoting.
func powerDNSUpsertArgs(params DNSRecordUpsertParams) []string {
	relName := strings.TrimSuffix(params.Name, "."+params.Zone)
	args := []string{}
	if ctxName := strings.TrimSpace(params.KubernetesContext); ctxName != "" {
		args = append(args, "--context", ctxName)
	}
	deployment := strings.TrimSpace(params.PowerDNSDeployment)
	if deployment == "" {
		deployment = TenantResourcePrefix("") + "-powerdns"
	}
	args = append(args, "-n", params.PlatformNamespace, "exec", "deploy/"+deployment, "--",
		"pdnsutil", "--config-dir="+powerDNSConfigDir, "replace-rrset",
		params.Zone, relName, params.Type, strconv.Itoa(params.TTL), params.Value)
	return args
}

// ingressApplyArgs is shared by the dry-run trace and the live exec. Reading the
// manifest from stdin (`-f -`) keeps a temp-file path out of the argv, so the trace
// stays deterministic.
func ingressApplyArgs(params IngressApplyParams) []string {
	args := []string{}
	if ctxName := strings.TrimSpace(params.KubernetesContext); ctxName != "" {
		args = append(args, "--context", ctxName)
	}
	args = append(args, "-n", params.Namespace, "apply", "-f", "-")
	return args
}

// upsertPowerDNSRecord is live-only: it must never run under a dry-run.
func upsertPowerDNSRecord(params DNSRecordUpsertParams) error {
	if out, err := Command("kubectl", powerDNSUpsertArgs(params)...).CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl exec pdnsutil replace-rrset: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// applyHostRoutingIngress is live-only: RunExposeService short-circuits before
// calling it, so it never runs under a dry-run.
func applyHostRoutingIngress(params IngressApplyParams) error {
	cmd := Command("kubectl", ingressApplyArgs(params)...)
	cmd.Stdin = strings.NewReader(renderHostRoutingIngress(params))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply ingress: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func renderHostRoutingIngress(params IngressApplyParams) string {
	return fmt.Sprintf(`apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: %s
  namespace: %s
  labels:
    app.kubernetes.io/managed-by: erun-expose
spec:
  rules:
    - host: %s
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: %s
                port:
                  number: %d
`, params.Name, params.Namespace, params.Host, params.ServiceName, params.ServicePort)
}
