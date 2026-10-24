locals {
  delegated_zone_name = "${var.subdomain}.${var.base_domain_name}"

  parent_zone_id = var.parent_zone_id != "" ? var.parent_zone_id : try(data.cloudflare_zone.parent[0].zone_id, "")
}

data "cloudflare_zone" "parent" {
  count = var.parent_zone_id == "" ? 1 : 0

  filter = {
    name    = var.base_domain_name
    account = { id = var.cloudflare_account_id }
  }
}

# Authority for the child zone lives in the platform's PowerDNS, not Cloudflare.
resource "cloudflare_dns_record" "delegation" {
  for_each = toset(var.nameservers)

  zone_id = local.parent_zone_id
  name    = local.delegated_zone_name
  type    = "NS"
  ttl     = var.delegation_ttl
  content = each.value
  comment = var.comment

  lifecycle {
    precondition {
      condition     = local.parent_zone_id != ""
      error_message = "Could not resolve the parent zone id: pass parent_zone_id, or set cloudflare_account_id so base_domain_name can be looked up."
    }
  }
}

# Glue for in-bailiwick nameservers, so resolving them does not loop back
# through the delegation.
resource "cloudflare_dns_record" "glue" {
  for_each = var.glue_records

  zone_id = local.parent_zone_id
  name    = each.key
  type    = "A"
  ttl     = var.glue_ttl
  content = each.value
  proxied = false
  comment = var.comment
}
