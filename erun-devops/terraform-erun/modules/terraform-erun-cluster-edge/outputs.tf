output "issuer_name" {
  description = "Name of the namespaced cert-manager Issuer (in issuer_namespace) that issues TLS certs via the DNS-01 challenge. Reference it from a same-namespace Certificate as `issuerRef: {kind: Issuer, name: <this>}` (or the `cert-manager.io/issuer` annotation)."
  value       = var.issuer_name
}

output "issuer_namespace" {
  description = "Namespace the Issuer, its DNS-01 credential Secret, and the edge's certs live in (the env namespace, or the cert-manager namespace for an apex-only edge)."
  value       = local.issuer_namespace
}

output "wildcard_certificate_secret" {
  description = "Name of the Secret the wildcard *.<services_zone> certificate is stored in (in var.namespace), or null when wildcard_certificate_enabled is false. Mount/reference it for TLS termination."
  value       = var.wildcard_certificate_enabled ? local.wildcard_secret_name : null
}

output "ingress_class" {
  description = "Ingress class to put on Ingress objects routed through this edge (\"traefik\" when this module installed it; otherwise the cluster's existing class)."
  value       = var.install_ingress_controller ? "traefik" : null
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
  value       = var.install_coredns_forward
}
