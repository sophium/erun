locals {
  cloudflare_token_secret = "${var.issuer_name}-cloudflare-token"
  wildcard_cert_name      = "${var.issuer_name}-wildcard"
  wildcard_secret_name    = "${var.issuer_name}-wildcard-tls"
  use_rfc2136             = var.dns01_provider == "powerdns-rfc2136"
  tsig_secret_name        = "${var.issuer_name}-rfc2136-tsig"
  # Per-env wildcard: *.<env_label>.<services_zone> → Secret <env_label>-wildcard-tls
  # in the env namespace, which `erun expose` references from its Ingress.
  env_namespace       = var.env_namespace != "" ? var.env_namespace : var.env_label
  per_env_cert_name   = var.env_label != "" ? "${var.env_label}-wildcard" : ""
  per_env_secret_name = var.env_label != "" ? "${var.env_label}-wildcard-tls" : ""
}

# Namespace that holds cert-manager, the Cloudflare token Secret, the
# ClusterIssuer's ACME account key, and the wildcard Certificate. Created here
# (not via helm create_namespace) so the Secret + issuer chart have a home even
# when install_cert_manager is false (an existing cert-manager elsewhere).
resource "kubernetes_namespace" "cert_manager" {
  metadata {
    name = var.namespace
  }
}

# Ingress controller. Optional: skip on a cluster that already has one.
resource "helm_release" "traefik" {
  count = var.install_ingress_controller ? 1 : 0

  name             = "traefik"
  repository       = "https://traefik.github.io/charts"
  chart            = "traefik"
  version          = var.traefik_chart_version
  namespace        = var.ingress_namespace
  create_namespace = true
}

# cert-manager (with its CRDs). Optional: skip when the cluster already runs it.
resource "helm_release" "cert_manager" {
  count = var.install_cert_manager ? 1 : 0

  name       = "cert-manager"
  repository = "https://charts.jetstack.io"
  chart      = "cert-manager"
  version    = var.cert_manager_chart_version
  namespace  = kubernetes_namespace.cert_manager.metadata[0].name

  set {
    name  = "crds.enabled"
    value = "true"
  }
}

# The Cloudflare API token cert-manager's DNS-01 solver reads. Materialized into
# a Secret because cert-manager (unlike the cloudflare Terraform provider) needs
# it in-cluster; the ClusterIssuer references it by name + key.
resource "kubernetes_secret" "cloudflare_api_token" {
  count = local.use_rfc2136 ? 0 : 1

  metadata {
    name      = local.cloudflare_token_secret
    namespace = kubernetes_namespace.cert_manager.metadata[0].name
  }
  data = {
    "api-token" = var.cloudflare_api_token
  }
  type = "Opaque"

  lifecycle {
    precondition {
      condition     = var.cloudflare_api_token != ""
      error_message = "cloudflare_api_token is required when dns01_provider is \"cloudflare\"."
    }
  }
}

# TSIG key material cert-manager's rfc2136 solver signs DNS UPDATE with. Minted by
# the erun-powerdns chart, read back, and passed in; materialized here so the
# ClusterIssuer can reference it by name in the cert-manager namespace.
resource "kubernetes_secret" "rfc2136_tsig" {
  count = local.use_rfc2136 ? 1 : 0

  metadata {
    name      = local.tsig_secret_name
    namespace = kubernetes_namespace.cert_manager.metadata[0].name
  }
  data = {
    "tsig-secret" = var.rfc2136_tsig_secret
  }
  type = "Opaque"

  lifecycle {
    precondition {
      condition     = var.rfc2136_tsig_secret != "" && var.powerdns_nameserver != "" && var.rfc2136_tsig_key_name != ""
      error_message = "dns01_provider \"powerdns-rfc2136\" requires rfc2136_tsig_secret, powerdns_nameserver, and rfc2136_tsig_key_name."
    }
  }
}

# The ClusterIssuer (and optional wildcard Certificate) ride in as a tiny local
# chart rather than kubernetes_manifest: the cert-manager CRDs do not exist at
# plan time on a first apply, which kubernetes_manifest cannot tolerate, whereas
# helm renders + applies the CRs after cert-manager (depends_on) has installed
# the CRDs. First-party providers only — no kubectl provider.
resource "helm_release" "issuer" {
  name      = "${var.issuer_name}-issuer"
  chart     = "${path.module}/chart-issuer"
  namespace = kubernetes_namespace.cert_manager.metadata[0].name

  set {
    name  = "issuerName"
    value = var.issuer_name
  }
  set {
    name  = "acmeEmail"
    value = var.acme_email
  }
  set {
    name  = "acmeServer"
    value = var.acme_server
  }
  set {
    name  = "servicesZone"
    value = var.services_zone
  }
  set {
    name  = "cloudflareTokenSecret"
    value = local.cloudflare_token_secret
  }
  set {
    name  = "certNamespace"
    value = kubernetes_namespace.cert_manager.metadata[0].name
  }
  set {
    name  = "wildcardCertificateEnabled"
    value = tostring(var.wildcard_certificate_enabled)
  }
  set {
    name  = "wildcardCertName"
    value = local.wildcard_cert_name
  }
  set {
    name  = "wildcardSecretName"
    value = local.wildcard_secret_name
  }
  set {
    name  = "dns01Provider"
    value = var.dns01_provider
  }
  set {
    name  = "rfc2136Nameserver"
    value = var.powerdns_nameserver
  }
  set {
    name  = "rfc2136TsigKeyName"
    value = var.rfc2136_tsig_key_name
  }
  set {
    name  = "rfc2136TsigAlgorithm"
    value = var.rfc2136_tsig_algorithm
  }
  set {
    name  = "rfc2136TsigSecretName"
    value = local.tsig_secret_name
  }
  set {
    name  = "perEnvCertificateEnabled"
    value = tostring(var.per_env_certificate_enabled)
  }
  set {
    name  = "perEnvCertName"
    value = local.per_env_cert_name
  }
  set {
    name  = "perEnvCertNamespace"
    value = local.env_namespace
  }
  set {
    name  = "perEnvSecretName"
    value = local.per_env_secret_name
  }
  set {
    name  = "envLabel"
    value = var.env_label
  }

  # The CRDs must exist (cert-manager installed) before the issuer/cert apply.
  depends_on = [helm_release.cert_manager, kubernetes_secret.cloudflare_api_token, kubernetes_secret.rfc2136_tsig]
}
