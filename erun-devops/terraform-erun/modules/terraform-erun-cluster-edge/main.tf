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
  arg_cloudflare_api_token                   = var.cloudflare_api_token == null ? "" : var.cloudflare_api_token
  arg_acme_server                            = var.acme_server == null ? "https://acme-v02.api.letsencrypt.org/directory" : var.acme_server
  arg_install_ingress_controller             = var.install_ingress_controller == null ? true : var.install_ingress_controller
  arg_install_cert_manager                   = var.install_cert_manager == null ? true : var.install_cert_manager
  arg_wildcard_certificate_enabled           = var.wildcard_certificate_enabled == null ? true : var.wildcard_certificate_enabled
  arg_namespace                              = var.namespace == null ? "cert-manager" : var.namespace
  arg_ingress_namespace                      = var.ingress_namespace == null ? "traefik" : var.ingress_namespace
  arg_issuer_name                            = var.issuer_name == null ? "erun-cloudflare" : var.issuer_name
  arg_cert_manager_chart_version             = var.cert_manager_chart_version == null ? "v1.20.3" : var.cert_manager_chart_version
  arg_traefik_chart_version                  = var.traefik_chart_version == null ? "33.2.1" : var.traefik_chart_version
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

# Transport policy for the public edge, declared once at the only layer that
# sees every public host. Traefik answers :80 for every rule it routes, so a
# host nobody remembered to annotate -- or an application behind the edge that
# issues a *relative* redirect, which inherits whatever scheme the browser
# started on -- serves and stays on plaintext by omission. Upgrading at the
# entrypoint means a host added later cannot opt out of https by forgetting.
locals {
  # permanent=true renders 301 rather than Traefik's default 302: this is a
  # standing policy, not a momentary move, and 301 is what an already-correct
  # host in the same estate answers with.
  #
  # The entrypoint-wide form has no path predicate. Traefik evaluates it for
  # every request entering the plaintext entrypoint, /.well-known/acme-challenge/
  # included, so it is safe exactly where every certificate for every host the
  # edge fronts is solved over DNS-01. This module's own Issuer always is
  # (chart-issuer is DNS-01 on every dns01_provider). An Issuer the *caller*
  # brings need not be, and that is not observable here: an HTTP-01 solver
  # answers on the plaintext entrypoint, Let's Encrypt follows this 301 to
  # https, and the solver's Ingress has no TLS block to answer on -- renewals
  # fail. http01_acme_challenges_present is how a caller states that fact, and
  # the refusal on edge_transport_policy is what keeps it from being assumed
  # away; acme_challenge_path_exempt is the shape that keeps the path reachable.
  traefik_redirect_args = local.arg_http_redirect_enabled && !local.arg_acme_challenge_path_exempt ? [
    "--entryPoints.web.http.redirections.entryPoint.to=websecure",
    "--entryPoints.web.http.redirections.entryPoint.scheme=https",
    "--entryPoints.web.http.redirections.entryPoint.permanent=true",
  ] : []

  # HSTS is a commitment, not a hint: once a browser has read it, it refuses
  # plaintext for that host for stsSeconds and the header cannot be recalled
  # early. The default is deliberately short (one day) and excludes
  # subdomains and preload, so a TLS gap on any name under the domain is a
  # one-day window rather than a year-long outage for visitors who already
  # have the header. Raise max-age -- then includeSubDomains, then preload --
  # once every host under the domain is verified https-only; note that
  # includeSubDomains is not just "stronger": it binds names this module does
  # not serve, and preload bakes the commitment into browsers.
  hsts_middleware_name = "erun-edge-hsts"
  hsts_middleware_ref  = "${local.arg_ingress_namespace}-${local.hsts_middleware_name}@kubernetescrd"

  traefik_hsts_args = local.arg_hsts_enabled ? [
    "--entryPoints.websecure.http.middlewares=${local.hsts_middleware_ref}",
  ] : []

  # Off first, then on: an entrypoint middleware that does not exist is
  # reported by Traefik and the response is served without the header, so the
  # middleware has to be a real object in the release rather than an argument
  # that promises one. extraObjects rides in the same Helm release as the
  # controller, which installs Traefik's CRDs (crds/) before its templates, so
  # the CR is never applied before its type exists.
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

  # The redirect as a per-request object rather than a per-entrypoint argument,
  # so the ACME challenge path can be carved out of it. A Middleware carries no
  # rule of its own -- it is inert until a router names it, and the router's
  # rule is the only place a path exclusion can live. Hence the pair: one
  # redirectScheme Middleware, and one catch-all router on the plaintext
  # entrypoint whose rule is "every path except the challenge prefix". The
  # redirect route terminates at noop@internal, Traefik's own service that
  # answers a middleware-only route without a backend. The router's priority is
  # not optional either -- see acme_exempt_redirect_priority, immediately below,
  # for why the default makes the whole pair inert.
  # No @kubernetescrd ref local here, unlike the HSTS Middleware: that one is
  # named by an entrypoint argument, which needs the fully-qualified form, while
  # this one is named by the router beside it, in the same namespace.
  redirect_middleware_name = "erun-edge-http-redirect"
  acme_challenge_prefix    = "/.well-known/acme-challenge/"

  # Explicit, because the default is the failure. Traefik derives a router's
  # priority from the length of its rule when none is declared, which leaves
  # this catch-all competing on its own 43 characters against the host routers
  # it exists to redirect. A `Host(<name>) && PathPrefix(/)` rule is 27
  # characters longer than the hostname in it, so every one for a name of 17
  # characters or more outranks this rule -- `console.` and `auth.` in the
  # tenant this was reported from among them. Traefik routes a matching request
  # to the longest rule, so the plaintext request reaches the application and
  # this redirect never runs: with no priority the exemption is silently inert
  # on exactly the hosts it was added for, and the plan still reports success.
  #
  # The entrypoint-wide form this replaces is not exposed to that, and this is
  # what has to be reproduced by hand: Traefik builds its own redirect router at
  # Priority = MaxInt - 1 (RedirectEntryPoint's default), above the MaxInt - 1000
  # ceiling it enforces on every user-defined router, so nothing a caller
  # declares can outrank it. 1000000 sits under that ceiling on 32-bit as well
  # as 64-bit, and clear of any rule length a real hostname can produce.
  #
  # Raising this above every other user router is safe only because the rule is
  # negated: a request under the challenge prefix does not match this route at
  # all, so no priority it carries can take a solver's request away from the
  # host router that answers it. The challenge path is unaffected either way.
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

  # Assembled with concat rather than as one conditional over the pair:
  # Terraform unifies a conditional's two result types, and a Middleware and an
  # IngressRoute share no common type, so `cond ? [middleware, router] : []` is
  # refused as an inconsistent conditional rather than rendering empty.
  acme_exempt_redirect_objects = concat(
    local.acme_exempt_redirect_enabled ? [local.acme_exempt_redirect_middleware] : [],
    local.acme_exempt_redirect_enabled ? [local.acme_exempt_redirect_router] : [],
  )

  # What rides on the controller this module installs. Empty when it manages no
  # policy: the switches above still describe the policy -- edge_transport_policy
  # hands it to whatever controller is already there -- but the module claims
  # none of it, so it neither configures a controller nor reports a failure.
  traefik_args = local.arg_manage_transport_policy ? concat(local.traefik_redirect_args, local.traefik_hsts_args) : []

  # The object half of the policy, carried the same way as the HSTS Middleware
  # and for the same reason: extraObjects rides in the release that installs
  # Traefik's own CRDs, so neither CR is applied before its type exists. Gated
  # one object per conditional for the same typing reason as above.
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

# The CoreDNS Corefile, read back so the forward can refuse to be a silent
# no-op. The whole mechanism depends on the Corefile importing
# /etc/coredns/custom/*.server; on a distribution or a hand-edited Corefile
# without that import, the ConfigMap applies cleanly, this module reports
# success, and in-cluster resolution is completely unchanged (#1165). Since the
# failure it prevents only shows up at certificate issuance or renewal, that
# false success can go unnoticed for weeks.
data "kubernetes_config_map" "coredns" {
  count = local.arg_install_coredns_forward ? 1 : 0

  metadata {
    name      = local.arg_coredns_configmap_name
    namespace = "kube-system"
  }
}

# coredns-custom is k3s's general extension point, not this module's private
# object, so ownership is split deliberately:
#
#   * this resource owns the object's EXISTENCE and nothing inside it, and
#   * kubernetes_config_map_v1_data below owns exactly one key.
#
# The previous shape owned the whole object, which made the module's own
# documented remedy destructive: it told an operator with a hand-applied
# coredns-custom to `terraform import` it, and the very next apply then pruned
# every key the module did not know about (#1165). ignore_changes on data is
# what stops that -- without it this resource's (empty) data map is
# authoritative and prunes on every apply.
#
# Set manage_coredns_custom_configmap = false on a cluster where something else
# owns the object's lifecycle; the module then manages only its own key and
# never creates or deletes coredns-custom.
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

# The one key this module owns. Server-side apply with an explicit field
# manager, so a key another operator or component put in the same ConfigMap is
# left strictly alone -- and so destroying the edge removes this key rather than
# the whole shared object.
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

    # Refuse to apply a forward the Corefile will never read. This is the
    # difference between a configuration that works and one that merely applies.
    precondition {
      condition     = can(regex("import\\s+/etc/coredns/custom/\\*\\.server", data.kubernetes_config_map.coredns[0].data["Corefile"]))
      error_message = "CoreDNS's Corefile does not import /etc/coredns/custom/*.server, so a coredns-custom entry would be written and never read — in-cluster resolution of ${local.arg_base_domain_name} would be unchanged while this module reported success. Add `import /etc/coredns/custom/*.server` to the Corefile (k3s does this by default), or set install_coredns_forward = false and solve node-resolver dependence another way."
    }
  }
}

# The forward used to be one resource owning the whole ConfigMap. Renaming it
# without this would destroy coredns-custom and recreate it, which on the
# platform's own cluster is a DNS outage window for every name the forward
# serves. The moved block carries the existing object into its new address
# instead, and the v1_data resource then adopts the key it already contains.
moved {
  from = kubernetes_config_map.coredns_forward
  to   = kubernetes_config_map.coredns_custom
}

# The local-path provisioner is the only storage class this platform's clusters
# ship, and it does BOTH provisioning and reclamation through a short-lived
# helper pod that it runs on the node holding the volume -- the node that is by
# definition under DiskPressure, and therefore tainted
# node.kubernetes.io/disk-pressure:NoSchedule.
#
# A TOLERATION IS NOT WHAT THE HELPER POD IS MISSING, and adding one fixes
# nothing. The provisioner appends that toleration itself -- `lpvTolerations`,
# `{Key: node.kubernetes.io/disk-pressure, Operator: Exists, Effect:
# NoSchedule}`, appended when the template declares none (v0.0.32 and later) or
# a blanket `{Operator: Exists}` appended unconditionally (v0.0.31 and
# earlier). So the helper pod has always tolerated the taint, and it is still
# rejected -- because the rejection is not the scheduler's. The provisioner
# pins the helper pod with `helperPod.Spec.NodeName = o.Node`, bypassing the
# scheduler entirely, so the only admission that applies is kubelet's, and
# kubelet's eviction manager turns away a pod whose node conditions are
# non-empty unless the pod is critical: `Admit` returns early on
# `kubelettypes.IsCriticalPod(attrs.Pod)`, and otherwise rejects with "The node
# had condition: [DiskPressure]". `IsCriticalPod` is true only for a static or
# mirror pod, or one whose resolved `spec.priority` is at least
# `scheduling.SystemCriticalPriority` (2e9). A template with no
# priorityClassName yields an ordinary zero-priority BestEffort pod, so the
# provisioner waits out its 120s create timeout and retries forever -- and
# `evictPod` refuses a critical pod the same way, so the class is what also
# stops the pod being evicted once it is running. Either way the volume is never
# provisioned and a deleted PVC frees no bytes, because reclamation runs the
# same helper: the node cannot free the space causing the pressure it would
# have to relieve to run the thing that frees the space.
#
# system-node-critical is what carries the pod past that admission and past
# eviction. (system-cluster-critical clears the same `>= SystemCriticalPriority`
# bar; the node-scoped class is the right one for a workload that exists only on
# one node.) The toleration is written out alongside it not because it is doing
# the work but because on v0.0.32 and later setting `tolerations` at all
# suppresses the provisioner's own default appending -- so once this template
# declares the disk-pressure toleration explicitly, that line is what keeps it,
# and a template whose own tolerations this module preserves must not lose it.
# On v0.0.31 and earlier the blanket default is still appended unconditionally,
# which makes the explicit entry redundant there rather than wrong.
#
# This is configuration rather than a patch to a workload this module does not
# own: the provisioner reads its helper pod spec from the helperPod.yaml key of
# its own ConfigMap. That key is MERGED, never replaced -- the image and every
# other field come from whatever the distribution shipped, so a mirrored or
# air-gapped cluster keeps the helper image its own manifest chose. Replacing
# the key outright would mean naming the helper image here, and a wrong one
# breaks provisioning cluster-wide, a strictly worse failure than this one.
data "kubernetes_config_map" "local_path" {
  count = local.arg_local_path_helper_pod_resilience ? 1 : 0

  metadata {
    name      = local.arg_local_path_configmap_name
    namespace = "kube-system"
  }
}

locals {
  # The distribution's own helper pod template, parsed. Empty when the feature
  # is off or the key is absent, so every expression below stays total.
  local_path_helper_pod_template = try(yamldecode(data.kubernetes_config_map.local_path[0].data["helperPod.yaml"]), {})

  # The template with the priority class and the toleration merged in. The
  # provisioner rejects a template that defines its own securityContext,
  # volumes, volumeMounts or serviceAccountName, so this adds nothing but the
  # two scheduling fields it is documented to accept.
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

# The one key this module owns inside the provisioner's ConfigMap. Server-side
# apply with an explicit field manager for the same reason as the CoreDNS
# forward above: the object is the distribution's, not this module's, so a key
# it manages for its own purposes must survive this apply untouched, and
# destroying the edge must remove this key rather than the whole object.
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
    # Refuse to write a key the provisioner will never read. A distribution
    # whose manifest predates helperPod.yaml support ships no such key, and the
    # provisioner then ignores whatever is written here: the apply succeeds,
    # this module reports success, and the helper pod is exactly as
    # inadmissible as before. The presence of the key in the distribution's own
    # template is the one signal available at plan time that this version reads
    # it, which is what makes it the precondition rather than a guess.
    precondition {
      condition     = contains(keys(data.kubernetes_config_map.local_path[0].data), "helperPod.yaml")
      error_message = "The ${local.arg_local_path_configmap_name} ConfigMap in kube-system carries no helperPod.yaml key, so this cluster's local-path provisioner does not read a helper pod template: writing one here would apply cleanly, report success, and leave the helper pod just as inadmissible under pressure as it is now. This is a provisioner older than helperPod.yaml support; upgrade the distribution's local-path provisioner, or set install_local_path_helper_pod_resilience = false and treat storage reclamation on a pressured node as the manual step it remains."
    }
  }
}

# Writing the key is not enough on its own: the provisioner reads its helper pod
# template once, when it starts, and holds it for the life of the process, so a
# module that wrote only the ConfigMap would apply cleanly, report success, and
# change nothing until the provisioner happened to restart -- the false success
# this module refuses elsewhere.
#
# The provisioner does have a 30s reload loop, `watchAndRefreshConfig` calling
# `refreshHelperPod`, and on the upstream manifest it would pick the corrected
# key up on its own. It is a no-op on k3s, which is the distribution this
# platform's clusters run: `refreshHelperPod` returns immediately unless
# CONFIG_MOUNT_PATH is set, and k3s's Deployment at
# /var/lib/rancher/k3s/server/manifests/local-storage.yaml declares only
# POD_NAMESPACE. So on k3s the template read at startup is the one that sticks,
# and the restart has to come from here. That is also why this annotates the
# Deployment rather than relying on a ConfigMap-mounted path: the startup read
# goes through the API (`findConfigFileFromConfigMap`), not the mount.
#
# Annotating the provisioner's pod template with a digest of the template makes
# the rollout a consequence of the configuration it depends on: the annotation
# changes only when the helper pod spec changes, so an unchanged apply is a
# no-op and a changed one restarts exactly the component that caches it.
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

# Ingress controller. Optional: skip on a cluster that already has one -- but
# then this module has nowhere to put its transport policy, which is a
# configuration the caller has to name rather than one that applies cleanly and
# does nothing (edge_transport_policy, outputs.tf). The refusal rides on an
# output because that is the only address here that always exists: a resource
# with count = 0 is never evaluated, so this release cannot carry the refusal
# that explains its own absence.
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
