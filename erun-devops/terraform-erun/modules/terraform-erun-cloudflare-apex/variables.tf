variable "cloudflare_account_id" {
  description = "Cloudflare account ID that owns base_domain_name. Only used to look up the zone when parent_zone_id is not supplied."
  type        = string
  default     = ""
}

variable "base_domain_name" {
  description = "The platform's own apex domain, already authoritative in Cloudflare (e.g. the platform config's basedomain, \"example.com\"). This module never creates that zone — it only writes records into it — because the apex zone already exists by the time a platform is delegating a services subzone out of it. Distinct from terraform-erun-cloudflare-services, whose base_domain_name is the *parent* of a new child zone it creates."
  type        = string

  validation {
    condition     = can(regex("^([a-z0-9]([a-z0-9-]*[a-z0-9])?\\.)+[a-z]{2,}$", var.base_domain_name))
    error_message = "base_domain_name must be a valid DNS domain such as \"example.com\"."
  }
}

variable "parent_zone_id" {
  description = "Cloudflare zone ID of base_domain_name. Leave empty to look it up by name (requires cloudflare_account_id)."
  type        = string
  default     = ""
}

variable "ip_address" {
  description = "IPv4 address the apex (and, when manage_www, the www) A record resolves to — the cluster's ingress IP."
  type        = string

  validation {
    condition     = can(regex("^(\\d{1,3}\\.){3}\\d{1,3}$", var.ip_address))
    error_message = "ip_address must be a valid IPv4 dotted-quad address."
  }
}

variable "manage_www" {
  description = "Also write a \"www\" A record alongside the apex one, pointing at the same ip_address. Set false for a platform whose apex is used for something else (e.g. a marketing site) that still wants console/auth hosts managed elsewhere in the zone, but not a www redirect."
  type        = bool
  default     = true
}

variable "record_ttl" {
  description = "TTL in seconds for the apex and www A records."
  type        = number
  default     = 300
}

variable "proxied" {
  description = "Whether the apex/www A records are proxied through Cloudflare (orange-cloud)."
  type        = bool
  default     = false
}
