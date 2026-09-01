package eruncommon

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// powerDNSConfigDir holds the PowerDNS backend config and DB password, so the
// pdnsutil exec carries no connection flags and interpolates no secret.
const powerDNSConfigDir = "/etc/pdns-shared"

// powerDNSExecPrefix is shared by every pdnsutil invocation (upsert, delete):
// the kubectl exec argv up to and including pdnsutil's --config-dir flag,
// which is what makes pdnsutil read the shared PowerDNS config instead of its
// own default. A second hand-written invocation without this flag is exactly
// how the flag gets forgotten — pdnsutil then reports the zone itself
// as missing rather than reporting that it looked in the wrong place.
func powerDNSExecPrefix(kubernetesContext, platformNamespace, powerDNSDeployment string) []string {
	args := []string{}
	if ctxName := strings.TrimSpace(kubernetesContext); ctxName != "" {
		args = append(args, "--context", ctxName)
	}
	deployment := strings.TrimSpace(powerDNSDeployment)
	if deployment == "" {
		deployment = TenantResourcePrefix("") + "-powerdns"
	}
	return append(args, "-n", platformNamespace, "exec", "deploy/"+deployment, "--",
		"pdnsutil", "--config-dir="+powerDNSConfigDir)
}

// powerDNSUpsertArgs is shared by the dry-run trace and the live exec so they stay
// identical. Tokens pass as direct argv (no `sh -c`), so the wildcard name needs no
// shell quoting.
func powerDNSUpsertArgs(params DNSRecordUpsertParams) []string {
	relName := strings.TrimSuffix(params.Name, "."+params.Zone)
	args := powerDNSExecPrefix(params.KubernetesContext, params.PlatformNamespace, params.PowerDNSDeployment)
	return append(args, "replace-rrset", params.Zone, relName, params.Type, strconv.Itoa(params.TTL), params.Value)
}

// powerDNSDeleteArgs is the delete-side counterpart to powerDNSUpsertArgs,
// built from the same powerDNSExecPrefix so the --config-dir flag
// can never drift between the two.
func powerDNSDeleteArgs(params DNSRecordDeleteParams) []string {
	relName := strings.TrimSuffix(params.Name, "."+params.Zone)
	args := powerDNSExecPrefix(params.KubernetesContext, params.PlatformNamespace, params.PowerDNSDeployment)
	return append(args, "delete-rrset", params.Zone, relName, params.Type)
}

// upsertPowerDNSRecord is live-only: it must never run under a dry-run.
func upsertPowerDNSRecord(params DNSRecordUpsertParams) error {
	if out, err := Command("kubectl", powerDNSUpsertArgs(params)...).CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl exec pdnsutil replace-rrset: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// deletePowerDNSRecord is live-only: RunUnexposeService short-circuits before
// calling it, so it never runs under a dry-run.
func deletePowerDNSRecord(params DNSRecordDeleteParams) error {
	if out, err := Command("kubectl", powerDNSDeleteArgs(params)...).CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl exec pdnsutil delete-rrset: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// applyHostRoutingIngress is live-only: RunExposeService short-circuits before
// calling it, so it never runs under a dry-run.
func applyHostRoutingIngress(params IngressApplyParams) error {
	cmd := Command("kubectl", kubectlApplyStdinArgs(params.Namespace, params.KubernetesContext)...)
	cmd.Stdin = strings.NewReader(renderHostRoutingIngress(params))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply ingress: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// traceTLSCertPlan traces the per-env TLS provisioning plan a dry-run would
// perform: the token Secret's apply command only (its content is a credential
// and stays redacted, matching applyMCPAuthSecret), and the Issuer/Certificate
// commands plus their non-secret manifests in full.
func traceTLSCertPlan(ctx Context, params TLSCertApplyParams) {
	args := kubectlApplyStdinArgs(params.Namespace, params.KubernetesContext)
	ctx.Trace(fmt.Sprintf("expose: tls: dns01 token secret %s (namespace %s, token read from %s, content redacted)", params.TokenSecretName, params.Namespace, params.Cert.DNS01TokenPath))
	ctx.TraceCommand("", "kubectl", args...)
	ctx.Trace(fmt.Sprintf("expose: tls: namespaced issuer %s -> broker %s (subzone %s.%s)", params.IssuerName, params.Cert.DNS01BrokerURL, params.EnvLabel, params.ServicesZone))
	ctx.TraceCommand("", "kubectl", args...)
	ctx.TraceBlock("expose: tls: issuer manifest", renderPerEnvIssuer(params))
	ctx.Trace(fmt.Sprintf("expose: tls: certificate %s -> secret %s", params.WildcardHost, params.SecretName))
	ctx.TraceCommand("", "kubectl", args...)
	ctx.TraceBlock("expose: tls: certificate manifest", renderPerEnvCertificate(params))
}

// applyTLSCertPlan is live-only: RunExposeService short-circuits before
// calling it, so it never runs under a dry-run. Applies in dependency order —
// the token Secret and Issuer must exist before the Certificate that
// references them, though cert-manager would simply retry a Certificate
// created first.
func applyTLSCertPlan(params TLSCertApplyParams) error {
	if err := applyDNS01TokenSecret(params); err != nil {
		return err
	}
	if err := applyPerEnvIssuer(params); err != nil {
		return err
	}
	return applyPerEnvCertificate(params)
}

func applyDNS01TokenSecret(params TLSCertApplyParams) error {
	manifest, err := renderDNS01TokenSecret(params)
	if err != nil {
		return err
	}
	args := kubectlApplyStdinArgs(params.Namespace, params.KubernetesContext)
	return applySecretManifest(params.KubernetesContext, params.Namespace, "dns01 token secret", manifest, args)
}

// applyPerEnvIssuer and applyPerEnvCertificate stay on the subprocess path
// even when the Secret above takes the library one: a cert-manager custom
// resource has no typed patch metadata, so kubectl merges it by a different
// algorithm reached through discovery and a dynamic client (see
// kubectlSecretApplyExecutionOperation).
func applyPerEnvIssuer(params TLSCertApplyParams) error {
	cmd := Command("kubectl", kubectlApplyStdinArgs(params.Namespace, params.KubernetesContext)...)
	cmd.Stdin = strings.NewReader(renderPerEnvIssuer(params))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply per-env issuer: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func applyPerEnvCertificate(params TLSCertApplyParams) error {
	cmd := Command("kubectl", kubectlApplyStdinArgs(params.Namespace, params.KubernetesContext)...)
	cmd.Stdin = strings.NewReader(renderPerEnvCertificate(params))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply per-env certificate: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// renderDNS01TokenSecret reads the broker token from the file
// TLSCertParams.DNS01TokenPath names and carries it under the key the
// provisioned Issuer's webhook solver config references. Read at apply time
// (never during dry-run) so the token itself never appears in a trace.
func renderDNS01TokenSecret(params TLSCertApplyParams) (string, error) {
	token, err := os.ReadFile(params.Cert.DNS01TokenPath)
	if err != nil {
		return "", fmt.Errorf("read dns01 token %s: %w", params.Cert.DNS01TokenPath, err)
	}
	return fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
  labels:
    app.kubernetes.io/managed-by: erun-expose
type: Opaque
stringData:
  token: %q
`, params.TokenSecretName, params.Namespace, strings.TrimSpace(string(token))), nil
}

// renderPerEnvIssuer is a namespaced Issuer, not a cluster-scoped
// ClusterIssuer: a Certificate can only reference an Issuer in its own
// namespace, so this env can never use another env's issuer, and another
// env's namespace-admin RBAC can never reach it either (a multi-tenant-
// safe shape, mirrored from terraform-erun-cluster-edge's chart-issuer). The
// selector scopes solving to this env's own subzone specifically — narrower
// than the broker's own per-token authorization, which is the real security
// boundary, but it means cert-manager itself never even offers this Issuer a
// challenge outside its own env.
func renderPerEnvIssuer(params TLSCertApplyParams) string {
	return fmt.Sprintf(`apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  name: %s
  namespace: %s
  labels:
    app.kubernetes.io/managed-by: erun-expose
spec:
  acme:
    email: %q
    server: %q
    privateKeySecretRef:
      name: %s-account-key
    solvers:
      - dns01:
          webhook:
            groupName: %q
            solverName: powerdns-broker
            config:
              brokerURL: %q
              tokenSecretRef:
                name: %s
                key: token
        selector:
          dnsZones:
            - %q
`, params.IssuerName, params.Namespace, params.Cert.ACMEEmail, params.Cert.acmeServer(), params.IssuerName,
		params.Cert.webhookGroupName(), params.Cert.DNS01BrokerURL, params.TokenSecretName, params.EnvLabel+"."+params.ServicesZone)
}

func renderPerEnvCertificate(params TLSCertApplyParams) string {
	return fmt.Sprintf(`apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: %s
  namespace: %s
  labels:
    app.kubernetes.io/managed-by: erun-expose
spec:
  secretName: %s
  issuerRef:
    name: %s
    kind: Issuer
  commonName: %q
  dnsNames:
    - %q
`, params.CertificateName, params.Namespace, params.SecretName, params.IssuerName, params.WildcardHost, params.WildcardHost)
}

func renderHostRoutingIngress(params IngressApplyParams) string {
	classBlock := ""
	if c := strings.TrimSpace(params.IngressClass); c != "" {
		classBlock = fmt.Sprintf("\n  ingressClassName: %s", c)
	}
	// TLS references the pre-issued per-env wildcard cert Secret (one cert per env
	// covers every exposed host) — no cert-manager annotation, so expose triggers
	// no per-host issuance.
	tlsBlock := ""
	if s := strings.TrimSpace(params.TLSSecretName); s != "" {
		tlsBlock = fmt.Sprintf("\n  tls:\n    - hosts:\n        - %s\n      secretName: %s", params.Host, s)
	}
	return fmt.Sprintf(`apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: %s
  namespace: %s
  labels:
    app.kubernetes.io/managed-by: erun-expose
spec:%s%s
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
`, params.Name, params.Namespace, classBlock, tlsBlock, params.Host, params.ServiceName, params.ServicePort)
}
