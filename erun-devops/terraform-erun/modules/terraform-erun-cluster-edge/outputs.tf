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
  description = "The edge's transport policy as configured: the redirect and HSTS entrypoint arguments (Traefik flag syntax), the HSTS Middleware object when hsts_enabled resolves true, and the ACME-exempting redirect objects when acme_challenge_path_exempt resolves true. Empty when the corresponding switch is off, and never null, so a caller can always read the shape it applies to its own controller. http01_acme_challenges_present echoes the declaration the policy was resolved against, which a caller that never sees this module's resources cannot otherwise read back."
  value = {
    manager                      = local.arg_manage_transport_policy ? "module" : "caller"
    http_redirect_enabled        = local.arg_http_redirect_enabled
    hsts_enabled                 = local.arg_hsts_enabled
    traefik_additional_arguments = concat(local.traefik_redirect_args, local.traefik_hsts_args)
    hsts_middleware              = local.arg_hsts_enabled ? local.hsts_middleware : null
    # The fact the module cannot observe, echoed back for the same reason as the
    # policy it governs: a bring-your-own-controller caller never sees this
    # module's resources, so this output is the only place it can read whether
    # the HTTP-01 condition it is carrying the policy *for* was declared at all.
    # Without it, a caller that handed the module a plan where the declaration
    # was set somewhere else -- an overlay, a wrapper, a variable file -- has no
    # way to tell whether the module agrees with it.
    http01_acme_challenges_present = local.arg_http01_acme_challenges_present
    # The redirect as objects rather than entrypoint arguments, present only
    # when acme_challenge_path_exempt resolves true. A caller applying this to
    # its own controller applies these alongside hsts_middleware; a caller that
    # declared http01_acme_challenges_present and sees this empty is holding
    # the blanket entrypoint redirect, which starves its HTTP-01 solver.
    acme_challenge_path_exempt = local.arg_acme_challenge_path_exempt
    redirect_objects           = local.acme_exempt_redirect_objects
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

  # The configuration this refuses: the caller has said an HTTP-01 Issuer
  # reaches this edge, and is still getting the entrypoint-wide redirect that
  # cannot be narrowed around the challenge path. Traefik applies it to
  # /.well-known/acme-challenge/ like any other path, so the certificate does
  # not renew -- and the failure surfaces weeks later, at renewal, as an expiry
  # rather than as a plan error. It is refused here rather than left to be
  # rediscovered because the module cannot see a foreign Issuer: only the
  # caller knows, and once they have said so, producing the shape that starves
  # it is a contradiction rather than a default.
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
