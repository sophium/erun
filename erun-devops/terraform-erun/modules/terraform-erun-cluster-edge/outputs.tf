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

# Lets a cluster with its own ingress controller (manage_transport_policy=false)
# apply the module's policy there instead of re-deriving it in an overlay.
output "edge_transport_policy" {
  description = "The edge's transport policy as configured: the redirect and HSTS entrypoint arguments (Traefik flag syntax), the HSTS Middleware object when hsts_enabled resolves true, and the ACME-exempting redirect objects when acme_challenge_path_exempt resolves true. Empty when the corresponding switch is off, and never null, so a caller can always read the shape it applies to its own controller. http01_acme_challenges_present echoes the declaration the policy was resolved against, which a caller that never sees this module's resources cannot otherwise read back."
  value = {
    manager                      = local.arg_manage_transport_policy ? "module" : "caller"
    http_redirect_enabled        = local.arg_http_redirect_enabled
    hsts_enabled                 = local.arg_hsts_enabled
    traefik_additional_arguments = concat(local.traefik_redirect_args, local.traefik_hsts_args)
    hsts_middleware              = local.arg_hsts_enabled ? local.hsts_middleware : null
    # The only way a bring-your-own-controller caller can read back the declaration.
    http01_acme_challenges_present = local.arg_http01_acme_challenges_present
    # A caller that declared http01_acme_challenges_present and sees this empty
    # is holding the blanket entrypoint redirect, which starves its HTTP-01 solver.
    acme_challenge_path_exempt = local.arg_acme_challenge_path_exempt
    redirect_objects           = local.acme_exempt_redirect_objects
  }

  # Without a controller nothing carries the policy, so the plan would apply
  # cleanly while every public host answers cleartext.
  precondition {
    condition     = local.arg_install_ingress_controller || !local.arg_manage_transport_policy
    error_message = "install_ingress_controller = false leaves this module with no controller to carry its transport policy, so http_redirect_enabled / hsts_enabled and the HSTS settings would be accepted and dropped: every public host would serve cleartext with no redirect and no Strict-Transport-Security, and this plan would report success. Either set install_ingress_controller = true, or set manage_transport_policy = false and apply the policy this module exposes at edge_transport_policy to the controller your cluster already runs."
  }

  # Traefik applies the entrypoint redirect to /.well-known/acme-challenge/ too,
  # so the certificate silently fails at renewal; only the caller can see a
  # foreign Issuer, so its declaration is enforced here.
  precondition {
    condition     = !local.arg_http01_acme_challenges_present || !local.arg_http_redirect_enabled || local.arg_acme_challenge_path_exempt
    error_message = "http01_acme_challenges_present = true says a certificate for a host this edge fronts is solved over HTTP-01, but the redirect is still the entrypoint-wide one, which Traefik applies to /.well-known/acme-challenge/ as well: the solver is redirected to https, its Ingress has no TLS block there, and the certificate fails to renew. Set acme_challenge_path_exempt = true to carry the redirect as an ACME-exempting router instead (exported at edge_transport_policy for a bring-your-own controller), or set http_redirect_enabled = false if nothing here should be redirected, or move those hosts to a DNS-01 Issuer and set http01_acme_challenges_present = false. If the HTTP-01 challenges instead never reach this edge's plaintext entrypoint -- those hosts are fronted by another controller, or the solver answers somewhere this entrypoint does not see -- then the fact being declared is not true of this edge and setting http01_acme_challenges_present = false is the correct remedy, not a way around this one; the declaration is what says the redirect here starves a solver, so it should be cleared only where the redirect is not in that solver's path."
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

output "local_path_helper_pod_resilience_installed" {
  description = "Whether this apply configured the local-path provisioner's helper pod to tolerate the disk-pressure taint (the resolved value of install_local_path_helper_pod_resilience). True means a node kubelet has tainted can still run the helper pod, so volumes can be provisioned and reclaimed while the node is under DiskPressure rather than only after it stops being."
  value       = local.arg_local_path_helper_pod_resilience
}
