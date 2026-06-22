variable "cloudflare_account_id" {
  description = "Cloudflare account ID that owns the delegated child zone."
  type        = string

  validation {
    condition     = length(var.cloudflare_account_id) > 0
    error_message = "cloudflare_account_id must not be empty."
  }
}

variable "base_domain_name" {
  description = "Parent domain that owns the delegation, e.g. \"example.com\". The module creates a child zone for \"<subdomain>.<base_domain_name>\"."
  type        = string

  validation {
    condition     = can(regex("^([a-z0-9]([a-z0-9-]*[a-z0-9])?\\.)+[a-z]{2,}$", var.base_domain_name))
    error_message = "base_domain_name must be a valid DNS domain such as \"example.com\"."
  }
}

variable "subdomain" {
  description = "Single DNS label delegated under base_domain_name, e.g. \"app\" to manage \"app.example.com\"."
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9]([a-z0-9-]*[a-z0-9])?$", var.subdomain))
    error_message = "subdomain must be a single DNS label (letters, digits, hyphens; no dots)."
  }
}

variable "ip_address" {
  description = "IPv4 address the service A record resolves to."
  type        = string

  validation {
    condition     = can(regex("^(\\d{1,3}\\.){3}\\d{1,3}$", var.ip_address))
    error_message = "ip_address must be a valid IPv4 dotted-quad address."
  }
}

variable "record_name" {
  description = "Host label for the A record inside the delegated zone. Empty string targets the zone apex (\"<subdomain>.<base_domain_name>\")."
  type        = string
  default     = ""
}

variable "manage_delegation" {
  description = "When true, resolve base_domain_name in Cloudflare and write the NS delegation records into the parent zone so the subdomain is delegated to the child zone's Cloudflare name servers. Set false when the parent zone lives outside this account/state; delegate manually using the name_servers output."
  type        = bool
  default     = true
}

variable "parent_zone_id" {
  description = "Cloudflare zone ID of base_domain_name. Leave empty to look it up by name. Ignored when manage_delegation is false."
  type        = string
  default     = ""
}

variable "record_ttl" {
  description = "TTL in seconds for the service A record."
  type        = number
  default     = 300
}

variable "delegation_ttl" {
  description = "TTL in seconds for the NS delegation records in the parent zone."
  type        = number
  default     = 172800
}

variable "proxied" {
  description = "Whether the service A record is proxied through Cloudflare (orange-cloud). Proxiable record types only; leave false for a plain DNS A record."
  type        = bool
  default     = false
}
