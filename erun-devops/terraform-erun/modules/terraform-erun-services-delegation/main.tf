locals {
  # Fully-qualified name of the delegated child zone, e.g. "services.example.com".
  delegated_zone_name = "${var.subdomain}.${var.base_domain_name}"

  # Resolve the parent zone id: an explicit input wins, otherwise the looked-up zone.
  parent_zone_id = var.parent_zone_id != "" ? var.parent_zone_id : try(data.cloudflare_zone.parent[0].zone_id, "")
}

# Look up the parent zone only when no id was supplied.
data "cloudflare_zone" "parent" {
  count = var.parent_zone_id == "" ? 1 : 0

  filter = {
    name    = var.base_domain_name
    account = { id = var.cloudflare_account_id }
  }
}

# Delegate the child zone to the platform's own PowerDNS nameservers by publishing
# one NS record per nameserver in the parent zone. Authority for the child lives
# outside Cloudflare, so no cloudflare_zone is created here.
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

# Glue A records for in-bailiwick nameservers, so a resolver following the
# delegation can resolve the nameserver FQDNs without looping back through it.
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
