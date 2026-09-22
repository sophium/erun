output "issuer_name" {
  description = "Name of the namespaced cert-manager Issuer (in issuer_namespace) that issues TLS certs via the DNS-01 challenge. Reference it from a same-namespace Certificate as `issuerRef: {kind: Issuer, name: <this>}` (or the `cert-manager.io/issuer` annotation)."
  value       = local.arg_issuer_name
}

output "issuer_namespace" {
  description = "Namespace the Issuer, its DNS-01 credential Secret, and the edge's certs live in (the env namespace, or the cert-manager namespace for an apex-only edge)."
  value       = local.issuer_namespace
}

output "wildcard_certificate_secret" {
  description = "Name of the Secret the wildcard *.<services_zone> certificate is stored in (in local.arg_namespace), or null when wildcard_certificate_enabled is false. Mount/reference it for TLS termination."
  value       = local.arg_wildcard_certificate_enabled ? local.wildcard_secret_name : null
}

output "ingress_class" {
  description = "Ingress class to put on Ingress objects routed through this edge (\"traefik\" when this module installed it; otherwise the cluster's existing class)."
  value       = local.arg_install_ingress_controller ? "traefik" : null
}

# The transport policy this module declares, as configured -- not as carried.
# A cluster that brings its own ingress controller sets
# manage_transport_policy=false and applies this to that controller, instead of
# re-deriving the redirect and the HSTS commitment in an overlay that then
# drifts from the module's own defaults.
output "edge_transport_policy" {
  description = "The edge's transport policy as configured: the redirect and HSTS entrypoint arguments (Traefik flag syntax), and the HSTS Middleware object when hsts_enabled resolves true. Empty when the corresponding switch is off, and never null, so a caller can always read the shape it applies to its own controller."
  value = {
    manager                      = local.arg_manage_transport_policy ? "module" : "caller"
    http_redirect_enabled        = local.arg_http_redirect_enabled
    hsts_enabled                 = local.arg_hsts_enabled
    traefik_additional_arguments = concat(local.traefik_redirect_args, local.traefik_hsts_args)
    hsts_middleware              = local.arg_hsts_enabled ? local.hsts_middleware : null
  }

  # The configuration this refuses: install_ingress_controller = false with the
  # policy still managed here. Nothing consumes the policy in that plan — no
  # controller is installed and no controller is told to apply it — so the
  # redirect and the HSTS header simply do not exist, while the plan applies
  # cleanly and every public host answers cleartext. The caller either installs
  # the controller or says, once, that the policy belongs to the one already
  # there.
  precondition {
    condition     = local.arg_install_ingress_controller || !local.arg_manage_transport_policy
    error_message = "install_ingress_controller = false leaves this module with no controller to carry its transport policy, so http_redirect_enabled / hsts_enabled and the HSTS settings would be accepted and dropped: every public host would serve cleartext with no redirect and no Strict-Transport-Security, and this plan would report success. Either set install_ingress_controller = true, or set manage_transport_policy = false and apply the policy this module exposes at edge_transport_policy to the controller your cluster already runs."
  }
}

output "namespace" {
  description = "Namespace holding cert-manager, the Cloudflare token Secret, and the wildcard Certificate."
  value       = kubernetes_namespace.cert_manager.metadata[0].name
}

output "dns01_webhook_installed" {
  description = "Whether this apply installed the per-cluster DNS-01 webhook shim (the resolved value of install_dns01_webhook, after applying its dns01_provider-based default). A per-tenant Issuer selecting the webhook solver (e.g. one erun expose provisions) can only reach Ready when this is true."
  value       = local.install_dns01_webhook
}

output "coredns_forward_installed" {
  description = "Whether this apply installed the CoreDNS custom forward zone for base_domain_name (the resolved value of install_coredns_forward). True means in-cluster resolution of the platform's own published names no longer depends on the node's resolver chain."
  value       = local.arg_install_coredns_forward
}
