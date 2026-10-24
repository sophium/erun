# Writes into the existing apex zone; unlike terraform-erun-cloudflare-services,
# it never creates the zone.
locals {
  zone_id = var.parent_zone_id != "" ? var.parent_zone_id : try(data.cloudflare_zone.apex[0].zone_id, "")
}

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
