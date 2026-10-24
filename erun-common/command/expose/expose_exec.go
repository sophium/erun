package expose

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// powerDNSConfigDir holds the PowerDNS config and DB password, so the exec
// carries no connection flags or secret.
const powerDNSConfigDir = "/etc/pdns-shared"

// powerDNSExecPrefix ends in --config-dir; without it pdnsutil reports the zone
// as missing rather than that it looked in the wrong place.
func powerDNSExecPrefix(kubernetesContext, platformNamespace, powerDNSDeployment string) []string {
	args := []string{}
	if ctxName := strings.TrimSpace(kubernetesContext); ctxName != "" {
		args = append(args, "--context", ctxName)
	}
	deployment := strings.TrimSpace(powerDNSDeployment)
	if deployment == "" {
		deployment = shared.TenantResourcePrefix("") + "-powerdns"
	}
	return append(args, "-n", platformNamespace, "exec", "deploy/"+deployment, "--",
		"pdnsutil", "--config-dir="+powerDNSConfigDir)
}

// powerDNSUpsertArgs is shared by the dry-run trace and the live exec; tokens
// pass as direct argv, so the wildcard needs no quoting.
func powerDNSUpsertArgs(params DNSRecordUpsertParams) []string {
	relName := strings.TrimSuffix(params.Name, "."+params.Zone)
	args := powerDNSExecPrefix(params.KubernetesContext, params.PlatformNamespace, params.PowerDNSDeployment)
	return append(args, "replace-rrset", params.Zone, relName, params.Type, strconv.Itoa(params.TTL), params.Value)
}

func powerDNSDeleteArgs(params DNSRecordDeleteParams) []string {
	relName := strings.TrimSuffix(params.Name, "."+params.Zone)
	args := powerDNSExecPrefix(params.KubernetesContext, params.PlatformNamespace, params.PowerDNSDeployment)
	return append(args, "delete-rrset", params.Zone, relName, params.Type)
}

// upsertPowerDNSRecord is live-only.
func upsertPowerDNSRecord(params DNSRecordUpsertParams) error {
	if out, err := core.Command("kubectl", powerDNSUpsertArgs(params)...).CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl exec pdnsutil replace-rrset: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// deletePowerDNSRecord is live-only.
func deletePowerDNSRecord(params DNSRecordDeleteParams) error {
	if out, err := core.Command("kubectl", powerDNSDeleteArgs(params)...).CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl exec pdnsutil delete-rrset: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// applyHostRoutingIngress is live-only.
func applyHostRoutingIngress(params IngressApplyParams) error {
	cmd := core.Command("kubectl", kube.KubectlApplyStdinArgs(params.Namespace, params.KubernetesContext)...)
	cmd.Stdin = strings.NewReader(renderHostRoutingIngress(params))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply ingress: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// traceTLSCertPlan keeps the token Secret's content redacted; the Issuer and
// Certificate manifests are traced in full.
func traceTLSCertPlan(ctx core.Context, params TLSCertApplyParams) {
	args := kube.KubectlApplyStdinArgs(params.Namespace, params.KubernetesContext)
	ctx.Trace(fmt.Sprintf("expose: tls: dns01 token secret %s (namespace %s, token read from %s, content redacted)", params.TokenSecretName, params.Namespace, params.Cert.DNS01TokenPath))
	ctx.TraceCommand("", "kubectl", args...)
	ctx.Trace(fmt.Sprintf("expose: tls: namespaced issuer %s -> broker %s (subzone %s.%s)", params.IssuerName, params.Cert.DNS01BrokerURL, params.EnvLabel, params.ServicesZone))
	ctx.TraceCommand("", "kubectl", args...)
	ctx.TraceBlock("expose: tls: issuer manifest", renderPerEnvIssuer(params))
	ctx.Trace(fmt.Sprintf("expose: tls: certificate %s -> secret %s", params.WildcardHost, params.SecretName))
	ctx.TraceCommand("", "kubectl", args...)
	ctx.TraceBlock("expose: tls: certificate manifest", renderPerEnvCertificate(params))
}

// applyTLSCertPlan is live-only and applies the token Secret and Issuer before
// the Certificate that references them.
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
	args := kube.KubectlApplyStdinArgs(params.Namespace, params.KubernetesContext)
	return kube.ApplySecretManifest(params.KubernetesContext, params.Namespace, "dns01 token secret", manifest, args)
}

// Issuer and Certificate stay on kubectl: a cert-manager CR has no typed patch
// metadata, so it merges by a different algorithm than the library Secret path.
func applyPerEnvIssuer(params TLSCertApplyParams) error {
	cmd := core.Command("kubectl", kube.KubectlApplyStdinArgs(params.Namespace, params.KubernetesContext)...)
	cmd.Stdin = strings.NewReader(renderPerEnvIssuer(params))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply per-env issuer: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func applyPerEnvCertificate(params TLSCertApplyParams) error {
	cmd := core.Command("kubectl", kube.KubectlApplyStdinArgs(params.Namespace, params.KubernetesContext)...)
	cmd.Stdin = strings.NewReader(renderPerEnvCertificate(params))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply per-env certificate: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// renderDNS01TokenSecret reads the token at apply time only, so it never appears in a trace.
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

// renderPerEnvIssuer is a namespaced Issuer so no other env can use or reach
// it; the selector also scopes solving to this env's subzone.
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
	// One pre-issued wildcard cert covers every host, so no cert-manager
	// annotation and no per-host issuance.
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
