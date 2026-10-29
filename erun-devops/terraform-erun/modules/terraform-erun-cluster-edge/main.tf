# Optional inputs default to null and resolve here once, so a wrapper can pass
# null for "use the default". Test `== null`: coalesce() also replaces "".
locals {
  arg_cloudflare_api_token                   = var.cloudflare_api_token == null ? "" : var.cloudflare_api_token
  arg_acme_server                            = var.acme_server == null ? "https://acme-v02.api.letsencrypt.org/directory" : var.acme_server
  arg_install_ingress_controller             = var.install_ingress_controller == null ? true : var.install_ingress_controller
  arg_install_cert_manager                   = var.install_cert_manager == null ? true : var.install_cert_manager
  arg_wildcard_certificate_enabled           = var.wildcard_certificate_enabled == null ? true : var.wildcard_certificate_enabled
  arg_namespace                              = var.namespace == null ? "cert-manager" : var.namespace
  arg_ingress_namespace                      = var.ingress_namespace == null ? "traefik" : var.ingress_namespace
  arg_issuer_name                            = var.issuer_name == null ? "erun-cloudflare" : var.issuer_name
  arg_cert_manager_chart_version             = var.cert_manager_chart_version == null ? "v1.20.5" : var.cert_manager_chart_version
  arg_traefik_chart_version                  = var.traefik_chart_version == null ? "42.1.1" : var.traefik_chart_version
  arg_dns01_provider                         = var.dns01_provider == null ? "cloudflare" : var.dns01_provider
  arg_dns01_webhook_image_pull_secrets       = var.dns01_webhook_image_pull_secrets == null ? [] : var.dns01_webhook_image_pull_secrets
  arg_broker_url                             = var.broker_url == null ? "" : var.broker_url
  arg_dns01_token_secret_name                = var.dns01_token_secret_name == null ? "" : var.dns01_token_secret_name
  arg_dns01_webhook_group_name               = var.dns01_webhook_group_name == null ? "acme.erun.io" : var.dns01_webhook_group_name
  arg_dns01_webhook_image                    = var.dns01_webhook_image == null ? "" : var.dns01_webhook_image
  arg_powerdns_nameserver                    = var.powerdns_nameserver == null ? "" : var.powerdns_nameserver
  arg_rfc2136_tsig_key_name                  = var.rfc2136_tsig_key_name == null ? "" : var.rfc2136_tsig_key_name
  arg_rfc2136_tsig_algorithm                 = var.rfc2136_tsig_algorithm == null ? "HMACSHA256" : var.rfc2136_tsig_algorithm
  arg_rfc2136_tsig_secret                    = var.rfc2136_tsig_secret == null ? "" : var.rfc2136_tsig_secret
  arg_per_env_certificate_enabled            = var.per_env_certificate_enabled == null ? false : var.per_env_certificate_enabled
  arg_env_label                              = var.env_label == null ? "" : var.env_label
  arg_env_namespace                          = var.env_namespace == null ? "" : var.env_namespace
  arg_install_coredns_forward                = var.install_coredns_forward == null ? false : var.install_coredns_forward
  arg_coredns_configmap_name                 = var.coredns_configmap_name == null ? "coredns" : var.coredns_configmap_name
  arg_manage_coredns_custom_configmap        = var.manage_coredns_custom_configmap == null ? true : var.manage_coredns_custom_configmap
  arg_base_domain_name                       = var.base_domain_name == null ? "" : var.base_domain_name
  arg_coredns_forward_upstreams              = var.coredns_forward_upstreams == null ? ["1.1.1.1", "1.0.0.1", "8.8.8.8"] : var.coredns_forward_upstreams
  arg_manage_transport_policy                = var.manage_transport_policy == null ? true : var.manage_transport_policy
  arg_http_redirect_enabled                  = var.http_redirect_enabled == null ? true : var.http_redirect_enabled
  arg_http01_acme_challenges_present         = var.http01_acme_challenges_present == null ? false : var.http01_acme_challenges_present
  arg_acme_challenge_path_exempt             = var.acme_challenge_path_exempt == null ? false : var.acme_challenge_path_exempt
  arg_hsts_enabled                           = var.hsts_enabled == null ? true : var.hsts_enabled
  arg_hsts_max_age_seconds                   = var.hsts_max_age_seconds == null ? 86400 : var.hsts_max_age_seconds
  arg_hsts_include_subdomains                = var.hsts_include_subdomains == null ? false : var.hsts_include_subdomains
  arg_hsts_preload                           = var.hsts_preload == null ? false : var.hsts_preload
  arg_local_path_helper_pod_resilience       = var.install_local_path_helper_pod_resilience == null ? false : var.install_local_path_helper_pod_resilience
  arg_local_path_configmap_name              = var.local_path_configmap_name == null ? "local-path-config" : var.local_path_configmap_name
  arg_local_path_provisioner_deployment_name = var.local_path_provisioner_deployment_name == null ? "local-path-provisioner" : var.local_path_provisioner_deployment_name
}

# Upgrading at the entrypoint means no host can stay on plaintext by omission.
locals {
  # The entrypoint-wide redirect also catches /.well-known/acme-challenge/, so it
  # is safe only when every certificate is solved over DNS-01
  # (see http01_acme_challenges_present).
  # A named target redirects to that entrypoint's listen port (the chart's :8443,
  # which nothing outside the pod reaches), so the target is the public port.
  traefik_redirect_args = local.arg_http_redirect_enabled && !local.arg_acme_challenge_path_exempt ? [
    "--entryPoints.web.http.redirections.entryPoint.to=:443",
    "--entryPoints.web.http.redirections.entryPoint.scheme=https",
    "--entryPoints.web.http.redirections.entryPoint.permanent=true",
  ] : []

  # HSTS cannot be recalled once a browser reads it, so the default is one day
  # without includeSubDomains or preload.
  hsts_middleware_name = "erun-edge-hsts"
  hsts_middleware_ref  = "${local.arg_ingress_namespace}-${local.hsts_middleware_name}@kubernetescrd"

  traefik_hsts_args = local.arg_hsts_enabled ? [
    "--entryPoints.websecure.http.middlewares=${local.hsts_middleware_ref}",
  ] : []

  # Traefik serves without the header if the entrypoint names a missing
  # middleware, so it ships in the same release, after Traefik's CRDs.
  hsts_middleware = {
    apiVersion = "traefik.io/v1alpha1"
    kind       = "Middleware"
    metadata = {
      name      = local.hsts_middleware_name
      namespace = local.arg_ingress_namespace
    }
    spec = {
      headers = {
        stsSeconds           = local.arg_hsts_max_age_seconds
        stsIncludeSubdomains = local.arg_hsts_include_subdomains
        stsPreload           = local.arg_hsts_preload
        forceSTSHeader       = true
      }
    }
  }

  # A Middleware has no rule, so the ACME exemption lives on a catch-all router
  # that excludes the challenge prefix and terminates at noop@internal.
  redirect_middleware_name = "erun-edge-http-redirect"
  acme_challenge_prefix    = "/.well-known/acme-challenge/"

  # Without an explicit priority, longer Host rules outrank this catch-all and the
  # redirect never runs. 1000000 stays under Traefik's MaxInt-1000 user ceiling on
  # 32-bit; the negated rule keeps challenge requests unaffected.
  acme_exempt_redirect_priority = 1000000

  acme_exempt_redirect_middleware = {
    apiVersion = "traefik.io/v1alpha1"
    kind       = "Middleware"
    metadata = {
      name      = local.redirect_middleware_name
      namespace = local.arg_ingress_namespace
    }
    spec = {
      redirectScheme = {
        scheme    = "https"
        permanent = true
      }
    }
  }

  acme_exempt_redirect_router = {
    apiVersion = "traefik.io/v1alpha1"
    kind       = "IngressRoute"
    metadata = {
      name      = local.redirect_middleware_name
      namespace = local.arg_ingress_namespace
    }
    spec = {
      entryPoints = ["web"]
      routes = [{
        match       = "!PathPrefix(`${local.acme_challenge_prefix}`)"
        kind        = "Rule"
        priority    = local.acme_exempt_redirect_priority
        middlewares = [{ name = local.redirect_middleware_name }]
        services    = [{ name = "noop@internal", kind = "TraefikService" }]
      }]
    }
  }

  acme_exempt_redirect_enabled = local.arg_http_redirect_enabled && local.arg_acme_challenge_path_exempt

  # concat, not one conditional: a Middleware and an IngressRoute share no type.
  acme_exempt_redirect_objects = concat(
    local.acme_exempt_redirect_enabled ? [local.acme_exempt_redirect_middleware] : [],
    local.acme_exempt_redirect_enabled ? [local.acme_exempt_redirect_router] : [],
  )

  # Empty when the module manages no policy; edge_transport_policy reports it instead.
  traefik_args = local.arg_manage_transport_policy ? concat(local.traefik_redirect_args, local.traefik_hsts_args) : []

  # One object per conditional, for the same typing reason.
  traefik_transport_objects = concat(
    local.arg_manage_transport_policy && local.arg_hsts_enabled ? [local.hsts_middleware] : [],
    local.arg_manage_transport_policy && local.acme_exempt_redirect_enabled ? [local.acme_exempt_redirect_middleware] : [],
    local.arg_manage_transport_policy && local.acme_exempt_redirect_enabled ? [local.acme_exempt_redirect_router] : [],
  )
}

locals {
  cloudflare_token_secret = "${local.arg_issuer_name}-cloudflare-token"
  wildcard_cert_name      = "${local.arg_issuer_name}-wildcard"
  wildcard_secret_name    = "${local.arg_issuer_name}-wildcard-tls"
  use_rfc2136             = local.arg_dns01_provider == "powerdns-rfc2136"
  use_broker              = local.arg_dns01_provider == "powerdns-broker"
  # Null installs the shim exactly when the platform's own Issuer uses the broker.
  install_dns01_webhook = var.install_dns01_webhook != null ? var.install_dns01_webhook : local.use_broker
  # Pinned by the same Chart.yaml as the module, so the image cannot drift from it.
  dns01_webhook_chart_app_version = yamldecode(file("${path.module}/chart-dns01-webhook/Chart.yaml")).appVersion
  dns01_webhook_image             = local.arg_dns01_webhook_image != "" ? local.arg_dns01_webhook_image : "ghcr.io/sophium/erun-dns01-webhook:${local.dns01_webhook_chart_app_version}"
  tsig_secret_name                = "${local.arg_issuer_name}-rfc2136-tsig"
  # `erun expose` references the per-env wildcard Secret from its Ingress.
  env_namespace       = local.arg_env_namespace != "" ? local.arg_env_namespace : local.arg_env_label
  per_env_cert_name   = local.arg_env_label != "" ? "${local.arg_env_label}-wildcard" : ""
  per_env_secret_name = local.arg_env_label != "" ? "${local.arg_env_label}-wildcard-tls" : ""
  # A namespaced Issuer only serves Certificates in its own namespace.
  issuer_namespace = local.env_namespace != "" ? local.env_namespace : local.arg_namespace

  # Named after the domain so it cannot collide with another *.server file.
  coredns_forward_key   = "${replace(local.arg_base_domain_name, ".", "-")}.server"
  coredns_forward_block = <<-EOT
    ${local.arg_base_domain_name}:53 {
        errors
        cache 30
        forward . ${join(" ", local.arg_coredns_forward_upstreams)}
    }
  EOT
}

# k3s CoreDNS forwards external names to the node resolver; this pins the platform's
# own names to known upstreams via the coredns-custom *.server import.

# Read back so the forward refuses to apply when the Corefile lacks the import,
# rather than silently changing nothing.
data "kubernetes_config_map" "coredns" {
  count = local.arg_install_coredns_forward ? 1 : 0

  metadata {
    name      = local.arg_coredns_configmap_name
    namespace = "kube-system"
  }
}

# coredns-custom is shared: this resource owns only the object's existence, and
# ignore_changes on data keeps keys it does not own from being pruned.
resource "kubernetes_config_map" "coredns_custom" {
  count = local.arg_install_coredns_forward && local.arg_manage_coredns_custom_configmap ? 1 : 0

  metadata {
    name      = "coredns-custom"
    namespace = "kube-system"
  }

  lifecycle {
    # Every key is owned by kubernetes_config_map_v1_data, key by key.
    ignore_changes = [data]

    precondition {
      condition     = local.arg_base_domain_name != ""
      error_message = "install_coredns_forward requires base_domain_name (the platform's own apex domain, e.g. \"example.com\")."
    }
  }
}

# Server-side apply owns only this key; destroy removes the key, not the object.
resource "kubernetes_config_map_v1_data" "coredns_forward" {
  count = local.arg_install_coredns_forward ? 1 : 0

  metadata {
    name      = "coredns-custom"
    namespace = "kube-system"
  }

  data = {
    (local.coredns_forward_key) = local.coredns_forward_block
  }

  field_manager = "erun-cluster-edge"
  force         = true

  depends_on = [kubernetes_config_map.coredns_custom]

  lifecycle {
    precondition {
      condition     = local.arg_base_domain_name != ""
      error_message = "install_coredns_forward requires base_domain_name (the platform's own apex domain, e.g. \"example.com\")."
    }

    # Refuse a forward the Corefile will never read.
    precondition {
      condition     = can(regex("import\\s+/etc/coredns/custom/\\*\\.server", data.kubernetes_config_map.coredns[0].data["Corefile"]))
      error_message = "CoreDNS's Corefile does not import /etc/coredns/custom/*.server, so a coredns-custom entry would be written and never read — in-cluster resolution of ${local.arg_base_domain_name} would be unchanged while this module reported success. Add `import /etc/coredns/custom/*.server` to the Corefile (k3s does this by default), or set install_coredns_forward = false and solve node-resolver dependence another way."
    }
  }
}

# Moves the old coredns_forward address so the rename does not recreate
# coredns-custom and cause a DNS outage.
moved {
  from = kubernetes_config_map.coredns_forward
  to   = kubernetes_config_map.coredns_custom
}

# The local-path helper pod runs on the DiskPressure node with NodeName set,
# bypassing the scheduler, so a toleration is not enough: kubelet admits it only
# as a critical pod. The explicit toleration keeps it on provisioner versions where
# setting tolerations suppresses the default. helperPod.yaml is merged, not
# replaced, so the distribution's helper image is kept.
data "kubernetes_config_map" "local_path" {
  count = local.arg_local_path_helper_pod_resilience ? 1 : 0

  metadata {
    name      = local.arg_local_path_configmap_name
    namespace = "kube-system"
  }
}

locals {
  # Empty when the feature is off or the key is absent.
  local_path_helper_pod_template = try(yamldecode(data.kubernetes_config_map.local_path[0].data["helperPod.yaml"]), {})

  # The provisioner rejects templates that set securityContext, volumes,
  # volumeMounts or serviceAccountName, so only scheduling fields are added.
  local_path_helper_pod = merge(local.local_path_helper_pod_template, {
    spec = merge(try(local.local_path_helper_pod_template.spec, {}), {
      priorityClassName = "system-node-critical"
      tolerations = concat(
        [for t in try(local.local_path_helper_pod_template.spec.tolerations, []) : t],
        [{
          key      = "node.kubernetes.io/disk-pressure"
          operator = "Exists"
          effect   = "NoSchedule"
        }],
      )
    })
  })
}

# Server-side apply owns only this key in the distribution's ConfigMap.
resource "kubernetes_config_map_v1_data" "local_path_helper_pod" {
  count = local.arg_local_path_helper_pod_resilience ? 1 : 0

  metadata {
    name      = local.arg_local_path_configmap_name
    namespace = "kube-system"
  }

  data = {
    "helperPod.yaml" = yamlencode(local.local_path_helper_pod)
  }

  field_manager = "erun-cluster-edge"
  force         = true

  lifecycle {
    # A distribution whose template lacks helperPod.yaml ignores the key, so its
    # absence must fail the apply rather than report false success.
    precondition {
      condition     = contains(keys(data.kubernetes_config_map.local_path[0].data), "helperPod.yaml")
      error_message = "The ${local.arg_local_path_configmap_name} ConfigMap in kube-system carries no helperPod.yaml key, so this cluster's local-path provisioner does not read a helper pod template: writing one here would apply cleanly, report success, and leave the helper pod just as inadmissible under pressure as it is now. This is a provisioner older than helperPod.yaml support; upgrade the distribution's local-path provisioner, or set install_local_path_helper_pod_resilience = false and treat storage reclamation on a pressured node as the manual step it remains."
    }
  }
}

# The provisioner reads its helper template only at startup (k3s sets no
# CONFIG_MOUNT_PATH, so its reload loop is a no-op). A digest annotation restarts
# it exactly when the template changes.
resource "kubernetes_annotations" "local_path_provisioner_rollout" {
  count = local.arg_local_path_helper_pod_resilience ? 1 : 0

  api_version = "apps/v1"
  kind        = "Deployment"

  metadata {
    name      = local.arg_local_path_provisioner_deployment_name
    namespace = "kube-system"
  }

  template_annotations = {
    "erun.io/helper-pod-template-digest" = sha256(yamlencode(local.local_path_helper_pod))
  }

  field_manager = "erun-cluster-edge"

  depends_on = [kubernetes_config_map_v1_data.local_path_helper_pod]
}

# Created explicitly so it exists even when install_cert_manager is false.
resource "kubernetes_namespace" "cert_manager" {
  metadata {
    name = local.arg_namespace
  }
}

# Optional; without it the transport policy has nowhere to go, which
# edge_transport_policy reports. Helm installs the chart's crds/ only when they
# are absent and never upgrades them, so moving an existing release to a newer
# chart leaves the cluster's Traefik CRDs where they were.
resource "helm_release" "traefik" {
  count = local.arg_install_ingress_controller ? 1 : 0

  name             = "traefik"
  repository       = "https://traefik.github.io/charts"
  chart            = "traefik"
  version          = local.arg_traefik_chart_version
  namespace        = local.arg_ingress_namespace
  create_namespace = true

  dynamic "set" {
    for_each = local.traefik_args
    content {
      name  = "additionalArguments[${set.key}]"
      value = set.value
    }
  }

  values = length(local.traefik_transport_objects) > 0 ? [yamlencode({ extraObjects = local.traefik_transport_objects })] : []
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

# cert-manager needs the token in-cluster, in the Issuer's namespace.
resource "kubernetes_secret" "cloudflare_api_token" {
  # Only the cloudflare solver reads this secret.
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

# Minted by erun-powerdns; materialized beside the namespaced Issuer.
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

# One per cluster, independent of the platform's own solver, so per-tenant
# Issuers can use the broker.
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

# A local chart, not kubernetes_manifest: cert-manager's CRDs do not exist at
# plan time on a first apply.
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

    # Only the platform's own Issuer reads these, and only in broker mode.
    precondition {
      condition     = !local.use_broker || (local.arg_broker_url != "" && local.arg_dns01_token_secret_name != "")
      error_message = "dns01_provider \"powerdns-broker\" solves the platform's own Issuer through the broker, so broker_url and dns01_token_secret_name are both required."
    }
  }

  # In broker mode the webhook's APIService must be registered first.
  depends_on = [helm_release.cert_manager, helm_release.dns01_webhook, kubernetes_secret.cloudflare_api_token, kubernetes_secret.rfc2136_tsig]
}
