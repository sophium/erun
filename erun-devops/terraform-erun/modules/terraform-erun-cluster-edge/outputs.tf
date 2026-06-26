output "cluster_issuer_name" {
  description = "Name of the cert-manager ClusterIssuer that issues TLS certs via the Cloudflare DNS-01 challenge. Reference it from an Ingress/Certificate as `cert-manager.io/cluster-issuer`."
  value       = var.issuer_name
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
