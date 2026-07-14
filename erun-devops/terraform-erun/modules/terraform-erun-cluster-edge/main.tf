locals {
  cloudflare_token_secret = "${var.issuer_name}-cloudflare-token"
  wildcard_cert_name      = "${var.issuer_name}-wildcard"
  wildcard_secret_name    = "${var.issuer_name}-wildcard-tls"
  use_rfc2136             = var.dns01_provider == "powerdns-rfc2136"
  use_broker              = var.dns01_provider == "powerdns-broker"
  tsig_secret_name        = "${var.issuer_name}-rfc2136-tsig"
  # Per-env wildcard: *.<env_label>.<services_zone> → Secret <env_label>-wildcard-tls
  # in the env namespace, which `erun expose` references from its Ingress.
  env_namespace       = var.env_namespace != "" ? var.env_namespace : var.env_label
  per_env_cert_name   = var.env_label != "" ? "${var.env_label}-wildcard" : ""
  per_env_secret_name = var.env_label != "" ? "${var.env_label}-wildcard-tls" : ""
  # The namespaced Issuer, its DNS-01 credential Secret, and the apex wildcard
  # cert all live here. A namespaced Issuer only serves Certificates in its own
  # namespace, so this is the env namespace (co-locating the per-env cert with
  # its issuer); it falls back to the cert-manager namespace for an apex-only
  # edge with no env.
  issuer_namespace = local.env_namespace != "" ? local.env_namespace : var.namespace
}

# Namespace cert-manager itself runs in. The Issuer, its DNS-01 credential
# Secret, its ACME account key, and the edge's certs live in issuer_namespace
# (the env namespace), not here. Created explicitly (not via helm
# create_namespace) so it has a home even when install_cert_manager is false (an
# existing cert-manager elsewhere).
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
# it in-cluster; the namespaced Issuer references it by name + key from its own
# namespace.
resource "kubernetes_secret" "cloudflare_api_token" {
  count = local.use_rfc2136 ? 0 : 1

  metadata {
    name      = local.cloudflare_token_secret
    namespace = local.issuer_namespace
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
# the erun-powerdns chart, read back, and passed in; materialized in the Issuer's
# namespace so the namespaced Issuer can reference it by name there.
resource "kubernetes_secret" "rfc2136_tsig" {
  count = local.use_rfc2136 ? 1 : 0

  metadata {
    name      = local.tsig_secret_name
    namespace = local.issuer_namespace
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

# The per-cluster cert-manager DNS-01 webhook shim (multi-tenant broker path).
# One per cluster: it forwards each per-tenant Issuer's challenge to the DNS-01
# broker, carrying the env's scoped token. Only installed for powerdns-broker;
# depends on cert-manager (its CRDs back the shim's serving-cert PKI).
resource "helm_release" "dns01_webhook" {
  count = local.use_broker ? 1 : 0

  name      = "${var.issuer_name}-dns01-webhook"
  chart     = "${path.module}/chart-dns01-webhook"
  namespace = kubernetes_namespace.cert_manager.metadata[0].name

  set {
    name  = "groupName"
    value = var.dns01_webhook_group_name
  }
  set {
    name  = "namespace"
    value = kubernetes_namespace.cert_manager.metadata[0].name
  }
  set {
    name  = "image"
    value = var.dns01_webhook_image
  }
  set {
    name  = "brokerURL"
    value = var.broker_url
  }

  lifecycle {
    precondition {
      condition     = var.broker_url != "" && var.dns01_webhook_image != "" && var.dns01_token_secret_name != ""
      error_message = "dns01_provider \"powerdns-broker\" requires broker_url, dns01_webhook_image, and dns01_token_secret_name."
    }
  }

  depends_on = [helm_release.cert_manager]
}

# The Issuer (and optional wildcard Certificate) ride in as a tiny local chart
# rather than kubernetes_manifest: the cert-manager CRDs do not exist at plan
# time on a first apply, which kubernetes_manifest cannot tolerate, whereas helm
# renders + applies the CRs after cert-manager (depends_on) has installed the
# CRDs. First-party providers only — no kubectl provider. The Helm release lives
# in the cert-manager namespace, but the Issuer and its certs set their own
# metadata.namespace (issuerNamespace) so they land in the env namespace.
resource "helm_release" "issuer" {
  name      = "${var.issuer_name}-issuer"
  chart     = "${path.module}/chart-issuer"
  namespace = kubernetes_namespace.cert_manager.metadata[0].name

  set {
    name  = "issuerName"
    value = var.issuer_name
  }
  set {
    name  = "issuerNamespace"
    value = local.issuer_namespace
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
    value = local.issuer_namespace
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
    name  = "dns01WebhookGroupName"
    value = var.dns01_webhook_group_name
  }
  set {
    name  = "brokerURL"
    value = var.broker_url
  }
  set {
    name  = "dns01TokenSecretName"
    value = var.dns01_token_secret_name
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

  # The CRDs must exist (cert-manager installed) before the issuer/cert apply;
  # in broker mode the webhook's APIService must be registered before a
  # per-tenant Issuer can solve a challenge through it.
  depends_on = [helm_release.cert_manager, helm_release.dns01_webhook, kubernetes_secret.cloudflare_api_token, kubernetes_secret.rfc2136_tsig]
}
