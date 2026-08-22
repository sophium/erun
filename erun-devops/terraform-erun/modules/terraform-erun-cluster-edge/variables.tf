variable "cloudflare_api_token" {
  description = "Cloudflare API token cert-manager's DNS-01 solver uses to prove control of the services zone, when dns01_provider is \"cloudflare\". The same account-scoped token erun injects as CLOUDFLARE_API_TOKEN (Zone:Read + DNS:Edit); pass it as TF_VAR_cloudflare_api_token. Materialized into a Kubernetes Secret because cert-manager needs it in-cluster. Leave empty when dns01_provider is \"powerdns-rfc2136\" or \"powerdns-broker\" (only the cloudflare solver reads this secret; a precondition still requires it for the cloudflare provider)."
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
  description = "Install cert-manager (and its CRDs). Set false on a cluster that already runs it; the Issuer is still created."
  type        = bool
  default     = true
}

variable "wildcard_certificate_enabled" {
  description = "Issue a wildcard Certificate for *.<services_zone> from the namespaced Issuer. Leave true in production; set false for a local apply with no real DNS zone (the Issuer is still created, it just issues nothing)."
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
  description = "Name of the namespaced cert-manager Issuer this module creates."
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
  description = "cert-manager DNS-01 solver used by the platform's OWN wildcard Issuer (this module's issuer_name Issuer) — not the per-cluster webhook shim, which install_dns01_webhook controls independently. \"cloudflare\" (default, back-compat) solves in the Cloudflare zone; \"powerdns-rfc2136\" solves via DNS UPDATE + TSIG directly against the self-hosted PowerDNS (single-tenant platform cluster only — the zone-wide TSIG key is an impersonation hole on a shared cluster); \"powerdns-broker\" routes the platform's own Issuer through the same brokered webhook path a multi-tenant platform's per-tenant Issuers use, authorized against the env's own subzone. A hosted platform typically keeps its own Issuer on \"cloudflare\" or \"powerdns-rfc2136\" while still setting install_dns01_webhook = true so the per-tenant brokered Issuers erun expose provisions can solve."
  type        = string
  default     = "cloudflare"

  validation {
    condition     = contains(["cloudflare", "powerdns-rfc2136", "powerdns-broker"], var.dns01_provider)
    error_message = "dns01_provider must be \"cloudflare\", \"powerdns-rfc2136\", or \"powerdns-broker\"."
  }
}

variable "install_dns01_webhook" {
  description = "Install the per-cluster cert-manager DNS-01 webhook shim (the powerdns-broker solver's APIService, RBAC, and serving-cert PKI) — independent of which solver the platform's own Issuer (dns01_provider) uses. Left unset (null), it defaults to true exactly when dns01_provider is \"powerdns-broker\", matching the module's prior behavior where the two were one switch. Set explicitly to install the shim for per-tenant brokered Issuers (e.g. the ones erun expose provisions) while the platform's own Issuer stays on \"cloudflare\" or \"powerdns-rfc2136\"."
  type        = bool
  default     = null
}

variable "dns01_webhook_image_pull_secrets" {
  description = "Names of image pull secrets for the DNS-01 webhook shim's image, in the cert-manager namespace. Empty for a public registry. Needed because the shim's image can be private while the rest of a release is not -- an unauthenticated pull leaves the APIService at MissingEndpoints, and a webhook that cannot start makes hosted environments undeletable."
  type        = list(string)
  default     = []
}

variable "broker_url" {
  description = "Base URL of the DNS-01 broker the webhook shim forwards to (the shim appends /present and /cleanup), e.g. \"https://api.frs-prod.services.example.com/v1/dns01\". Required when the webhook shim is installed (install_dns01_webhook)."
  type        = string
  default     = ""
}

variable "dns01_token_secret_name" {
  description = "Name of the Secret, in the env (Issuer) namespace, holding this env's DNS-01 broker token under key \"token\" (minted by the backend's POST /v1/environments/{id}/dns01-token). The per-tenant Issuer's webhook solver references it. Required when the webhook shim is installed (install_dns01_webhook)."
  type        = string
  default     = ""
}

variable "dns01_webhook_group_name" {
  description = "API group the per-cluster DNS-01 webhook shim registers under; the per-tenant Issuer's webhook solver selects it. A cluster singleton — keep stable."
  type        = string
  default     = "acme.erun.io"
}

variable "dns01_webhook_image" {
  description = "Container image (repository:tag) for the DNS-01 webhook shim, e.g. \"ghcr.io/sophium/erun-dns01-webhook:1.0.150\". Optional: left empty (the default), it resolves to ghcr.io/sophium/erun-dns01-webhook at the version chart-dns01-webhook/Chart.yaml (bundled in this module) is itself released at, so the shim can never disagree with the module it ships beside. Set it only to override that — e.g. to test a build ahead of a release."
  type        = string
  default     = ""
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
