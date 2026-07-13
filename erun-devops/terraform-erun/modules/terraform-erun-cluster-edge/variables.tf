variable "cloudflare_api_token" {
  description = "Cloudflare API token cert-manager's DNS-01 solver uses to prove control of the services zone, when dns01_provider is \"cloudflare\". The same account-scoped token erun injects as CLOUDFLARE_API_TOKEN (Zone:Read + DNS:Edit); pass it as TF_VAR_cloudflare_api_token. Materialized into a Kubernetes Secret because cert-manager needs it in-cluster. Leave empty when dns01_provider is \"powerdns-rfc2136\" (a precondition still requires it for the cloudflare provider)."
  type        = string
  sensitive   = true
  default     = ""
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
  description = "Pinned cert-manager Helm chart version. Keep >= v1.17.2: earlier versions delete the DNS-01 _acme-challenge TXT with an empty Cloudflare zone id, orphaning records and wedging issuance/renewal."
  type        = string
  default     = "v1.20.3"
}

variable "traefik_chart_version" {
  description = "Pinned Traefik Helm chart version."
  type        = string
  default     = "33.2.1"
}

variable "dns01_provider" {
  description = "cert-manager DNS-01 solver provider. \"cloudflare\" (default, back-compat) solves in the Cloudflare zone; \"powerdns-rfc2136\" solves via DNS UPDATE + TSIG directly against the self-hosted PowerDNS authoritative for the delegated services zone — use it once the zone is delegated off Cloudflare."
  type        = string
  default     = "cloudflare"

  validation {
    condition     = contains(["cloudflare", "powerdns-rfc2136"], var.dns01_provider)
    error_message = "dns01_provider must be \"cloudflare\" or \"powerdns-rfc2136\"."
  }
}

variable "powerdns_nameserver" {
  description = "host:port of the PowerDNS the RFC2136 solver sends DNS UPDATE to (e.g. \"erun-powerdns.frs-prod.svc.cluster.local:53\"). Required when dns01_provider is \"powerdns-rfc2136\"."
  type        = string
  default     = ""
}

variable "rfc2136_tsig_key_name" {
  description = "TSIG key name authorized for dynamic updates on the services zone (the erun-powerdns chart's tsig key-name). Required for powerdns-rfc2136."
  type        = string
  default     = ""
}

variable "rfc2136_tsig_algorithm" {
  description = "TSIG algorithm in cert-manager's form (e.g. HMACSHA256). PowerDNS uses its own lowercase form (hmac-sha256) internally; keep them the same algorithm."
  type        = string
  default     = "HMACSHA256"
}

variable "rfc2136_tsig_secret" {
  description = "Base64 TSIG key material (the erun-powerdns chart's tsig-secret, read back from its Secret). Materialized into a Secret cert-manager's rfc2136 solver references. Required for powerdns-rfc2136."
  type        = string
  sensitive   = true
  default     = ""
}

variable "per_env_certificate_enabled" {
  description = "Issue a per-env wildcard Certificate *.<env_label>.<services_zone> into the env namespace, whose Secret `erun expose` references for TLS. Enable in the platform/serving env."
  type        = bool
  default     = false
}

variable "env_label" {
  description = "The <tenant>-<env> label the per-env wildcard covers (e.g. \"frs-prod\" → *.frs-prod.<services_zone>). Required when per_env_certificate_enabled."
  type        = string
  default     = ""
}

variable "env_namespace" {
  description = "Namespace the per-env wildcard Certificate + its Secret live in (the env's own namespace, e.g. frs-prod), so a co-located Ingress can reference the Secret. Defaults to env_label when unset."
  type        = string
  default     = ""
}
