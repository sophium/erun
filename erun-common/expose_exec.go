package eruncommon

import (
	"fmt"
	"strconv"
	"strings"
)

// powerDNSConfigDir is where the erun-powerdns chart writes the generated
// gpgsql backend config (see erun-devops/k8s/erun-powerdns). pdnsutil in the
// PowerDNS pod reads the backend (and the postgres password) from there via
// --config-dir, so the exec carries no --gpgsql-* flags and no password.
const powerDNSConfigDir = "/etc/pdns-shared"

// powerDNSUpsertArgs builds the kubectl argv that replaces one record's rrset by
// exec'ing pdnsutil in the platform's PowerDNS pod. The pdnsutil tokens are
// passed as direct argv (no `sh -c`), so the wildcard name needs no shell
// quoting and no secret is interpolated. Shared by the dry-run trace and the
// live exec so they are identical.
func powerDNSUpsertArgs(params DNSRecordUpsertParams) []string {
	relName := strings.TrimSuffix(params.Name, "."+params.Zone)
	args := []string{}
	if ctxName := strings.TrimSpace(params.KubernetesContext); ctxName != "" {
		args = append(args, "--context", ctxName)
	}
	args = append(args, "-n", params.PlatformNamespace, "exec", "deploy/erun-powerdns", "--",
		"pdnsutil", "--config-dir="+powerDNSConfigDir, "replace-rrset",
		params.Zone, relName, params.Type, strconv.Itoa(params.TTL), params.Value)
	return args
}

// ingressApplyArgs builds the kubectl argv that applies the Host-routing Ingress
// into the target env's namespace, reading the manifest from stdin (`-f -`) so
// the command is deterministic (no temp-file path). Shared by trace and exec.
func ingressApplyArgs(params IngressApplyParams) []string {
	args := []string{}
	if ctxName := strings.TrimSpace(params.KubernetesContext); ctxName != "" {
		args = append(args, "--context", ctxName)
	}
	args = append(args, "-n", params.Namespace, "apply", "-f", "-")
	return args
}

// upsertPowerDNSRecord replaces the rrset for one record in the platform's
// services zone by exec'ing pdnsutil inside the PowerDNS pod, which reads the
// gpgsql backend (and password) from its generated --config-dir config.
// Live-only: never runs under a dry-run.
func upsertPowerDNSRecord(params DNSRecordUpsertParams) error {
	if out, err := Command("kubectl", powerDNSUpsertArgs(params)...).CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl exec pdnsutil replace-rrset: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// applyHostRoutingIngress renders and applies a networking.k8s.io/v1 Ingress
// that Host-routes the exposed hostname to the in-namespace Service, piping the
// manifest to `kubectl apply -f -`. Live-only: it shells out to kubectl against
// the env's cluster, so it never runs under a dry-run (RunExposeService
// short-circuits before calling it).
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
