locals {
  # Fully-qualified name of the delegated child zone, e.g. "app.example.com".
  delegated_zone_name = "${var.subdomain}.${var.base_domain_name}"

  # FQDN of the service A record; an empty record_name targets the zone apex.
  record_fqdn = var.record_name == "" ? local.delegated_zone_name : "${var.record_name}.${local.delegated_zone_name}"

  # Resolve the parent zone id: an explicit input wins, otherwise the looked-up zone.
  parent_zone_id = var.parent_zone_id != "" ? var.parent_zone_id : try(data.cloudflare_zone.parent[0].zone_id, "")
}

# Look up the parent zone only when delegation is managed here and no id was supplied.
data "cloudflare_zone" "parent" {
  count = var.manage_delegation && var.parent_zone_id == "" ? 1 : 0

  filter = {
    name    = var.base_domain_name
    account = { id = var.cloudflare_account_id }
  }
}

# The delegated child zone.
resource "cloudflare_zone" "this" {
  name = local.delegated_zone_name

  account = {
    id = var.cloudflare_account_id
  }
}

# Delegation: publish each of the child zone's name servers as an NS record in the
# parent zone so the subdomain is delegated to the child zone's Cloudflare name servers.
resource "cloudflare_dns_record" "delegation" {
  for_each = var.manage_delegation ? toset(cloudflare_zone.this.name_servers) : toset([])

  zone_id = local.parent_zone_id
  name    = local.delegated_zone_name
  type    = "NS"
  ttl     = var.delegation_ttl
  content = each.value
}

# Service A record inside the delegated zone.
resource "cloudflare_dns_record" "this" {
  zone_id = cloudflare_zone.this.id
  name    = local.record_fqdn
  type    = "A"
  ttl     = var.record_ttl
  content = var.ip_address
  proxied = var.proxied
}
