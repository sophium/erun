# Manages A records for the platform's own apex domain and (optionally) its
# "www" host, directly in the zone Cloudflare is already authoritative for —
# erun-zitadel's auth host and erun-console's console host both already live
# in this same apex zone (see their charts' "Cloudflare-managed apex zone, not
# the delegated services zone" notes), but until this module existed nothing
# declared those records: an operator added them by hand. This module does not
# create the zone itself (unlike terraform-erun-cloudflare-services, which
# creates a *new* delegated child zone) — the apex zone already exists by the
# time a platform delegates a services subzone out of it.
locals {
  zone_id = var.parent_zone_id != "" ? var.parent_zone_id : try(data.cloudflare_zone.apex[0].zone_id, "")
}

# Look up the existing apex zone only when no id was supplied.
data "cloudflare_zone" "apex" {
  count = var.parent_zone_id == "" ? 1 : 0

  filter = {
    name    = var.base_domain_name
    account = { id = var.cloudflare_account_id }
  }
}

resource "cloudflare_dns_record" "apex" {
  zone_id = local.zone_id
  name    = var.base_domain_name
  type    = "A"
  ttl     = var.record_ttl
  content = var.ip_address
  proxied = var.proxied

  lifecycle {
    precondition {
      condition     = local.zone_id != ""
      error_message = "Could not resolve the apex zone id: pass parent_zone_id, or set cloudflare_account_id so base_domain_name can be looked up."
    }
  }
}

resource "cloudflare_dns_record" "www" {
  count = var.manage_www ? 1 : 0

  zone_id = local.zone_id
  name    = "www.${var.base_domain_name}"
  type    = "A"
  ttl     = var.record_ttl
  content = var.ip_address
  proxied = var.proxied
}
