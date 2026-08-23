# Terraform does not substitute a variable's default when a caller assigns an
# explicit null: the null reaches the variable's own validation and its uses, so
# `length(null)` aborts outright and `null != ""` is *true*, silently selecting
# the wrong branch of a guard. A wrapper module cannot conditionally omit an
# argument in HCL, so "use this module's default" has to be expressible as null
# -- otherwise every wrapper must duplicate the default and can drift from it
# (#1161). Every optional input therefore declares `default = null` and resolves
# its effective value exactly once here.
#
# The test is `== null`, deliberately not coalesce(): coalesce skips empty
# strings as well as nulls, so it would silently replace a legitimately empty
# input with the default -- and coalesce("", "") is a hard error, which would
# abort every apply where an optional string is genuinely unset.
locals {
  arg_cloudflare_api_token             = var.cloudflare_api_token == null ? "" : var.cloudflare_api_token
  arg_acme_server                      = var.acme_server == null ? "https://acme-v02.api.letsencrypt.org/directory" : var.acme_server
  arg_install_ingress_controller       = var.install_ingress_controller == null ? true : var.install_ingress_controller
  arg_install_cert_manager             = var.install_cert_manager == null ? true : var.install_cert_manager
  arg_wildcard_certificate_enabled     = var.wildcard_certificate_enabled == null ? true : var.wildcard_certificate_enabled
  arg_namespace                        = var.namespace == null ? "cert-manager" : var.namespace
  arg_ingress_namespace                = var.ingress_namespace == null ? "traefik" : var.ingress_namespace
  arg_issuer_name                      = var.issuer_name == null ? "erun-cloudflare" : var.issuer_name
  arg_cert_manager_chart_version       = var.cert_manager_chart_version == null ? "v1.20.3" : var.cert_manager_chart_version
  arg_traefik_chart_version            = var.traefik_chart_version == null ? "33.2.1" : var.traefik_chart_version
  arg_dns01_provider                   = var.dns01_provider == null ? "cloudflare" : var.dns01_provider
  arg_dns01_webhook_image_pull_secrets = var.dns01_webhook_image_pull_secrets == null ? [] : var.dns01_webhook_image_pull_secrets
  arg_broker_url                       = var.broker_url == null ? "" : var.broker_url
  arg_dns01_token_secret_name          = var.dns01_token_secret_name == null ? "" : var.dns01_token_secret_name
  arg_dns01_webhook_group_name         = var.dns01_webhook_group_name == null ? "acme.erun.io" : var.dns01_webhook_group_name
  arg_dns01_webhook_image              = var.dns01_webhook_image == null ? "" : var.dns01_webhook_image
  arg_powerdns_nameserver              = var.powerdns_nameserver == null ? "" : var.powerdns_nameserver
  arg_rfc2136_tsig_key_name            = var.rfc2136_tsig_key_name == null ? "" : var.rfc2136_tsig_key_name
  arg_rfc2136_tsig_algorithm           = var.rfc2136_tsig_algorithm == null ? "HMACSHA256" : var.rfc2136_tsig_algorithm
  arg_rfc2136_tsig_secret              = var.rfc2136_tsig_secret == null ? "" : var.rfc2136_tsig_secret
  arg_per_env_certificate_enabled      = var.per_env_certificate_enabled == null ? false : var.per_env_certificate_enabled
  arg_env_label                        = var.env_label == null ? "" : var.env_label
  arg_env_namespace                    = var.env_namespace == null ? "" : var.env_namespace
  arg_install_coredns_forward          = var.install_coredns_forward == null ? false : var.install_coredns_forward
  arg_base_domain_name                 = var.base_domain_name == null ? "" : var.base_domain_name
  arg_coredns_forward_upstreams        = var.coredns_forward_upstreams == null ? ["1.1.1.1", "1.0.0.1", "8.8.8.8"] : var.coredns_forward_upstreams
}

locals {
  cloudflare_token_secret = "${local.arg_issuer_name}-cloudflare-token"
  wildcard_cert_name      = "${local.arg_issuer_name}-wildcard"
  wildcard_secret_name    = "${local.arg_issuer_name}-wildcard-tls"
  use_rfc2136             = local.arg_dns01_provider == "powerdns-rfc2136"
  use_broker              = local.arg_dns01_provider == "powerdns-broker"
  # install_dns01_webhook left unset (null) preserves the historical behavior
  # of installing the shim exactly when the platform's own Issuer uses the
  # broker solver; setting it explicitly decouples the two, so a platform can
  # install the shim for per-tenant brokered Issuers (e.g. erun expose's)
  # while its own Issuer stays on cloudflare or rfc2136.
  install_dns01_webhook = var.install_dns01_webhook != null ? var.install_dns01_webhook : local.use_broker
  # The shim ships as one release with this module — chart-dns01-webhook's own
  # Chart.yaml is stamped to the erun version at every release, the same way
  # every umbrella chart's dependency versions are. Reading it here is this
  # module's equivalent of a Helm template's `.Chart.AppVersion` default (see
  # erun-zitadel's bootstrap image): the shim's image can no longer disagree
  # with the module by construction, because they are pinned by the same file.
  # local.arg_dns01_webhook_image still overrides it, for testing a build ahead of a
  # release.
  dns01_webhook_chart_app_version = yamldecode(file("${path.module}/chart-dns01-webhook/Chart.yaml")).appVersion
  dns01_webhook_image             = local.arg_dns01_webhook_image != "" ? local.arg_dns01_webhook_image : "ghcr.io/sophium/erun-dns01-webhook:${local.dns01_webhook_chart_app_version}"
  tsig_secret_name                = "${local.arg_issuer_name}-rfc2136-tsig"
  # Per-env wildcard: *.<env_label>.<services_zone> → Secret <env_label>-wildcard-tls
  # in the env namespace, which `erun expose` references from its Ingress.
  env_namespace       = local.arg_env_namespace != "" ? local.arg_env_namespace : local.arg_env_label
  per_env_cert_name   = local.arg_env_label != "" ? "${local.arg_env_label}-wildcard" : ""
  per_env_secret_name = local.arg_env_label != "" ? "${local.arg_env_label}-wildcard-tls" : ""
  # The namespaced Issuer, its DNS-01 credential Secret, and the apex wildcard
  # cert all live here. A namespaced Issuer only serves Certificates in its own
  # namespace, so this is the env namespace (co-locating the per-env cert with
  # its issuer); it falls back to the cert-manager namespace for an apex-only
  # edge with no env.
  issuer_namespace = local.env_namespace != "" ? local.env_namespace : local.arg_namespace

  # CoreDNS custom server block for base_domain_name. File name is derived from
  # the domain (dots to dashes) rather than fixed, so it can't collide with an
  # unrelated *.server file someone else drops in the same ConfigMap.
  coredns_forward_key   = "${replace(local.arg_base_domain_name, ".", "-")}.server"
  coredns_forward_block = <<-EOT
    ${local.arg_base_domain_name}:53 {
        errors
        cache 30
        forward . ${join(" ", local.arg_coredns_forward_upstreams)}
    }
  EOT
}

# k3s's bundled CoreDNS ends its default Corefile in `forward . /etc/resolv.conf`,
# so every name outside cluster.local resolves through whatever DNS the node
# happens to use — including the platform's own published names, which makes
# cert-manager's HTTP-01 self-check (and every unattended renewal after it)
# hostage to a resolver outside the platform's control. k3s's CoreDNS already
# mounts coredns-custom at /etc/coredns/custom (optional) and imports every
# *.server file from it, so this needs no change to the CoreDNS Deployment.
#
# Owns the whole coredns-custom object rather than merging into it (e.g. via
# kubernetes_config_map_v1_data): that resource requires the ConfigMap to
# already exist, which a cluster that has never hand-created one — the normal
# case — does not have, so it can't be the thing that creates it either.
# Nothing else in this platform writes to coredns-custom; if that changes,
# extend this resource's data map from the caller rather than adding a second
# resource that would race it for ownership of the same object. A cluster
# already carrying a hand-applied coredns-custom must be reconciled once
# (terraform import, or delete the hand-applied copy) before the first apply
# with install_coredns_forward = true.
resource "kubernetes_config_map" "coredns_forward" {
  count = local.arg_install_coredns_forward ? 1 : 0

  metadata {
    name      = "coredns-custom"
    namespace = "kube-system"
  }

  data = {
    (local.coredns_forward_key) = local.coredns_forward_block
  }

  lifecycle {
    precondition {
      condition     = local.arg_base_domain_name != ""
      error_message = "install_coredns_forward requires base_domain_name (the platform's own apex domain, e.g. \"example.com\")."
    }
  }
}

# Namespace cert-manager itself runs in. The Issuer, its DNS-01 credential
# Secret, its ACME account key, and the edge's certs live in issuer_namespace
# (the env namespace), not here. Created explicitly (not via helm
# create_namespace) so it has a home even when install_cert_manager is false (an
# existing cert-manager elsewhere).
resource "kubernetes_namespace" "cert_manager" {
  metadata {
    name = local.arg_namespace
  }
}

# Ingress controller. Optional: skip on a cluster that already has one.
resource "helm_release" "traefik" {
  count = local.arg_install_ingress_controller ? 1 : 0

  name             = "traefik"
  repository       = "https://traefik.github.io/charts"
  chart            = "traefik"
  version          = local.arg_traefik_chart_version
  namespace        = local.arg_ingress_namespace
  create_namespace = true
}

# cert-manager (with its CRDs). Optional: skip when the cluster already runs it.
resource "helm_release" "cert_manager" {
  count = local.arg_install_cert_manager ? 1 : 0

  name       = "cert-manager"
  repository = "https://charts.jetstack.io"
  chart      = "cert-manager"
  version    = local.arg_cert_manager_chart_version
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
  # Only the cloudflare solver (chart-issuer's default branch) ever reads this
  # secret; rfc2136 and broker both reference a different credential.
  count = local.use_rfc2136 || local.use_broker ? 0 : 1

  metadata {
    name      = local.cloudflare_token_secret
    namespace = local.issuer_namespace
  }
  data = {
    "api-token" = local.arg_cloudflare_api_token
  }
  type = "Opaque"

  lifecycle {
    precondition {
      condition     = local.arg_cloudflare_api_token != ""
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
    "tsig-secret" = local.arg_rfc2136_tsig_secret
  }
  type = "Opaque"

  lifecycle {
    precondition {
      condition     = local.arg_rfc2136_tsig_secret != "" && local.arg_powerdns_nameserver != "" && local.arg_rfc2136_tsig_key_name != ""
      error_message = "dns01_provider \"powerdns-rfc2136\" requires rfc2136_tsig_secret, powerdns_nameserver, and rfc2136_tsig_key_name."
    }
  }
}

# The per-cluster cert-manager DNS-01 webhook shim (multi-tenant broker path).
# One per cluster: it forwards each per-tenant Issuer's challenge to the DNS-01
# broker, carrying the env's scoped token. Installed whenever
# install_dns01_webhook resolves true — independent of the platform's own
# Issuer solver, so a per-tenant Issuer elsewhere (e.g. erun expose's) can use
# the broker even while dns01_provider keeps the platform's own Issuer on
# cloudflare or rfc2136. Depends on cert-manager (its CRDs back the shim's
# serving-cert PKI).
resource "helm_release" "dns01_webhook" {
  count = local.install_dns01_webhook ? 1 : 0

  name      = "${local.arg_issuer_name}-dns01-webhook"
  chart     = "${path.module}/chart-dns01-webhook"
  namespace = kubernetes_namespace.cert_manager.metadata[0].name

  set {
    name  = "groupName"
    value = local.arg_dns01_webhook_group_name
  }
  set {
    name  = "namespace"
    value = kubernetes_namespace.cert_manager.metadata[0].name
  }
  set {
    name  = "image"
    value = local.dns01_webhook_image
  }
  set {
    name  = "brokerURL"
    value = local.arg_broker_url
  }

  dynamic "set" {
    for_each = local.arg_dns01_webhook_image_pull_secrets
    content {
      name  = "imagePullSecrets[${set.key}].name"
      value = set.value
    }
  }

  lifecycle {
    precondition {
      condition     = local.arg_broker_url != ""
      error_message = "install_dns01_webhook requires broker_url."
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
  name      = "${local.arg_issuer_name}-issuer"
  chart     = "${path.module}/chart-issuer"
  namespace = kubernetes_namespace.cert_manager.metadata[0].name

  set {
    name  = "issuerName"
    value = local.arg_issuer_name
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
    value = local.arg_acme_server
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
    value = tostring(local.arg_wildcard_certificate_enabled)
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
    value = local.arg_dns01_provider
  }
  set {
    name  = "rfc2136Nameserver"
    value = local.arg_powerdns_nameserver
  }
  set {
    name  = "rfc2136TsigKeyName"
    value = local.arg_rfc2136_tsig_key_name
  }
  set {
    name  = "rfc2136TsigAlgorithm"
    value = local.arg_rfc2136_tsig_algorithm
  }
  set {
    name  = "rfc2136TsigSecretName"
    value = local.tsig_secret_name
  }
  set {
    name  = "dns01WebhookGroupName"
    value = local.arg_dns01_webhook_group_name
  }
  set {
    name  = "brokerURL"
    value = local.arg_broker_url
  }
  set {
    name  = "dns01TokenSecretName"
    value = local.arg_dns01_token_secret_name
  }
  set {
    name  = "perEnvCertificateEnabled"
    value = tostring(local.arg_per_env_certificate_enabled)
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
    value = local.arg_env_label
  }

  lifecycle {
    precondition {
      condition     = !local.use_broker || local.install_dns01_webhook
      error_message = "dns01_provider \"powerdns-broker\" makes the platform's own Issuer solve through the DNS-01 webhook shim, but install_dns01_webhook resolved to false. Rendering that Certificate without the shim's APIService/RBAC leaves cert-manager denied at admission and the resulting namespace undeletable — leave install_dns01_webhook unset (it defaults to true here) or set it to true."
    }

    # Only the platform's own Issuer reads these, and only in broker mode. A
    # platform that installs the shim purely so tenant Issuers can solve names
    # no token secret of its own: those are per-env and land beside each
    # environment, not here.
    precondition {
      condition     = !local.use_broker || (local.arg_broker_url != "" && local.arg_dns01_token_secret_name != "")
      error_message = "dns01_provider \"powerdns-broker\" solves the platform's own Issuer through the broker, so broker_url and dns01_token_secret_name are both required."
    }
  }

  # The CRDs must exist (cert-manager installed) before the issuer/cert apply;
  # in broker mode the webhook's APIService must be registered before a
  # per-tenant Issuer can solve a challenge through it.
  depends_on = [helm_release.cert_manager, helm_release.dns01_webhook, kubernetes_secret.cloudflare_api_token, kubernetes_secret.rfc2136_tsig]
}
