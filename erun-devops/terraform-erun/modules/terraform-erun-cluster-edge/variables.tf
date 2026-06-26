variable "cloudflare_api_token" {
  description = "Cloudflare API token cert-manager's DNS-01 solver uses to prove control of the services zone. In production this is the same account-scoped token erun injects into the runtime pod as CLOUDFLARE_API_TOKEN (Zone:Read + DNS:Edit); pass it through as TF_VAR_cloudflare_api_token. Unlike the cloudflare provider (which reads the env var directly), cert-manager needs it materialized into a Kubernetes Secret, so it is an explicit sensitive input here."
  type        = string
  sensitive   = true

  validation {
    condition     = length(var.cloudflare_api_token) > 0
    error_message = "cloudflare_api_token must not be empty."
  }
}

variable "acme_email" {
  description = "Contact email for the ACME account (the platform config's acmeemail). Let's Encrypt sends expiry notices here."
  type        = string

  validation {
    condition     = can(regex("^[^@[:space:]]+@[^@[:space:]]+\\.[^@[:space:]]+$", var.acme_email))
    error_message = "acme_email must be a valid email address."
  }
}

variable "services_zone" {
  description = "The DNS zone the edge issues a wildcard certificate for, e.g. \"services.example.com\" (the platform config's serviceszone). cert-manager solves the DNS-01 challenge in this Cloudflare zone."
  type        = string

  validation {
    condition     = can(regex("^([a-z0-9]([a-z0-9-]*[a-z0-9])?\\.)+[a-z]{2,}$", var.services_zone))
    error_message = "services_zone must be a valid DNS domain such as \"services.example.com\"."
  }
}

variable "acme_server" {
  description = "ACME directory URL. Defaults to Let's Encrypt production; point at the staging directory (https://acme-staging-v02.api.letsencrypt.org/directory) while validating to avoid rate limits."
  type        = string
  default     = "https://acme-v02.api.letsencrypt.org/directory"
}

variable "install_ingress_controller" {
  description = "Install Traefik as the ingress controller. Set false on a cluster that already has one (then point routes at the existing ingressClassName)."
  type        = bool
  default     = true
}

variable "install_cert_manager" {
  description = "Install cert-manager (and its CRDs). Set false on a cluster that already runs it; the ClusterIssuer is still created."
  type        = bool
  default     = true
}

variable "wildcard_certificate_enabled" {
  description = "Issue a wildcard Certificate for *.<services_zone> from the ClusterIssuer. Leave true in production; set false for a local apply with no real Cloudflare zone (the ClusterIssuer is still created, it just issues nothing)."
  type        = bool
  default     = true
}

variable "namespace" {
  description = "Namespace cert-manager, the Cloudflare token Secret, and the wildcard Certificate live in."
  type        = string
  default     = "cert-manager"
}

variable "ingress_namespace" {
  description = "Namespace the Traefik ingress controller is installed into."
  type        = string
  default     = "traefik"
}

variable "issuer_name" {
  description = "Name of the cert-manager ClusterIssuer this module creates."
  type        = string
  default     = "erun-cloudflare"
}

variable "cert_manager_chart_version" {
  description = "Pinned cert-manager Helm chart version."
  type        = string
  default     = "v1.16.2"
}

variable "traefik_chart_version" {
  description = "Pinned Traefik Helm chart version."
  type        = string
  default     = "33.2.1"
}

variable "kubeconfig_path" {
  description = "Path to a kubeconfig for the target cluster. Empty uses the default loading rules (in-cluster service account in a pod, else KUBECONFIG / ~/.kube/config)."
  type        = string
  default     = ""
}
