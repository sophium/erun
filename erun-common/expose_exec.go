package eruncommon

import (
	"fmt"
	"os"
	"strings"
)

// applyHostRoutingIngress renders and applies a networking.k8s.io/v1 Ingress
// that Host-routes the exposed hostname to the in-namespace Service. Live-only:
// it shells out to kubectl against the env's cluster, so it never runs under a
// dry-run (RunExposeService short-circuits before calling it).
func applyHostRoutingIngress(params IngressApplyParams) error {
	manifest := renderHostRoutingIngress(params)
	file, err := os.CreateTemp("", "erun-expose-ingress-*.yaml")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err := file.WriteString(manifest); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	args := []string{}
	if ctxName := strings.TrimSpace(params.KubernetesContext); ctxName != "" {
		args = append(args, "--context", ctxName)
	}
	args = append(args, "-n", params.Namespace, "apply", "-f", file.Name())
	if out, err := Command("kubectl", args...).CombinedOutput(); err != nil {
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

// upsertPowerDNSRecord replaces the rrset for one record in the platform's
// services zone by exec'ing pdnsutil inside the PowerDNS pod, which holds the
// gpgsql password in its PDNS_GPGSQL_PASSWORD env (expanded by the in-pod sh).
// The gpgsql connection coordinates match the erun-powerdns chart defaults.
// Live-only: never runs under a dry-run.
func upsertPowerDNSRecord(params DNSRecordUpsertParams) error {
	relName := strings.TrimSuffix(params.Name, "."+params.Zone)
	script := fmt.Sprintf(
		`pdnsutil --launch=gpgsql --gpgsql-host=erun-postgres --gpgsql-port=5432 --gpgsql-dbname=powerdns --gpgsql-user=erun --gpgsql-password="$PDNS_GPGSQL_PASSWORD" replace-rrset %s %s %s %d %s`,
		shellSingleQuote(params.Zone), shellSingleQuote(relName), shellSingleQuote(params.Type), params.TTL, shellSingleQuote(params.Value),
	)
	args := []string{}
	if ctxName := strings.TrimSpace(params.KubernetesContext); ctxName != "" {
		args = append(args, "--context", ctxName)
	}
	args = append(args, "-n", params.PlatformNamespace, "exec", "deploy/erun-powerdns", "--", "sh", "-c", script)
	if out, err := Command("kubectl", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl exec pdnsutil replace-rrset: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// shellSingleQuote wraps a value in single quotes for safe interpolation into
// the in-pod `sh -c` script, escaping any embedded single quotes.
func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
