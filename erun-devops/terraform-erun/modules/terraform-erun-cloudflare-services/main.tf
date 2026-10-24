locals {
  delegated_zone_name = "${var.subdomain}.${var.base_domain_name}"

  # An empty record_name targets the zone apex.
  record_fqdn = var.record_name == "" ? local.delegated_zone_name : "${var.record_name}.${local.delegated_zone_name}"

  parent_zone_id = var.parent_zone_id != "" ? var.parent_zone_id : try(data.cloudflare_zone.parent[0].zone_id, "")
}

data "cloudflare_zone" "parent" {
  count = var.manage_delegation && var.parent_zone_id == "" ? 1 : 0

  filter = {
    name    = var.base_domain_name
    account = { id = var.cloudflare_account_id }
  }
}

resource "cloudflare_zone" "this" {
  name = local.delegated_zone_name

  account = {
    id = var.cloudflare_account_id
  }
}

resource "cloudflare_dns_record" "delegation" {
  for_each = var.manage_delegation ? toset(cloudflare_zone.this.name_servers) : toset([])

  zone_id = local.parent_zone_id
  name    = local.delegated_zone_name
  type    = "NS"
  ttl     = var.delegation_ttl
  content = each.value
}

resource "cloudflare_dns_record" "this" {
  zone_id = cloudflare_zone.this.id
  name    = local.record_fqdn
  type    = "A"
  ttl     = var.record_ttl
  content = var.ip_address
  proxied = var.proxied
}
