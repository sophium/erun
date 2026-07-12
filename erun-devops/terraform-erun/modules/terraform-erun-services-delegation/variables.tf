variable "base_domain_name" {
  description = "Parent domain that owns the delegation, e.g. \"example.com\". The module delegates \"<subdomain>.<base_domain_name>\" to external, self-hosted nameservers (the platform's PowerDNS) rather than to a Cloudflare-hosted child zone. Distinct from terraform-erun-cloudflare-services, which makes Cloudflare authoritative for the child."
  type        = string

  validation {
    condition     = can(regex("^([a-z0-9]([a-z0-9-]*[a-z0-9])?\\.)+[a-z]{2,}$", var.base_domain_name))
    error_message = "base_domain_name must be a valid DNS domain such as \"example.com\"."
  }
}

variable "subdomain" {
  description = "Single DNS label delegated under base_domain_name, e.g. \"services\" to delegate \"services.example.com\"."
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9]([a-z0-9-]*[a-z0-9])?$", var.subdomain))
    error_message = "subdomain must be a single DNS label (letters, digits, hyphens; no dots)."
  }
}

variable "nameservers" {
  description = "Authoritative nameserver FQDNs the child zone is delegated to (the self-hosted PowerDNS nameservers, e.g. [\"ns1.example.com\", \"ns2.example.com\"]). Written as NS records in the parent zone; must match the NS set the PowerDNS zone itself serves so parent and child agree."
  type        = list(string)

  validation {
    condition     = length(var.nameservers) > 0
    error_message = "nameservers must list at least one authoritative nameserver."
  }
}

variable "glue_records" {
  description = "Glue A records to publish in the parent zone: a map of nameserver FQDN to its IPv4 address. Required for in-bailiwick nameservers (a nameserver living inside base_domain_name, e.g. ns1.example.com serving example.com's own child) so resolvers can reach the delegation target without a lookup loop. Leave empty for out-of-zone nameservers that resolve elsewhere."
  type        = map(string)
  default     = {}

  validation {
    condition     = alltrue([for ip in values(var.glue_records) : can(regex("^(\\d{1,3}\\.){3}\\d{1,3}$", ip))])
    error_message = "every glue_records value must be a valid IPv4 dotted-quad address."
  }
}

variable "parent_zone_id" {
  description = "Cloudflare zone ID of base_domain_name. Leave empty to look it up by name (requires cloudflare_account_id)."
  type        = string
  default     = ""
}

variable "cloudflare_account_id" {
  description = "Cloudflare account ID owning base_domain_name. Only used to look up the parent zone when parent_zone_id is not supplied."
  type        = string
  default     = ""
}

variable "comment" {
  description = "Cloudflare record comment applied to every delegation and glue record, so the parent zone marks them as terraform-managed and a human is warned off hand-editing them. Managing it explicitly also keeps the plan free of comment drift. Empty string clears any comment."
  type        = string
  default     = ""
}

variable "delegation_ttl" {
  description = "TTL in seconds for the NS delegation records in the parent zone."
  type        = number
  default     = 172800
}

variable "glue_ttl" {
  description = "TTL in seconds for the glue A records in the parent zone."
  type        = number
  default     = 300
}
