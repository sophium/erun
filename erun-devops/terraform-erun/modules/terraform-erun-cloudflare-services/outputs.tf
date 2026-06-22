output "zone_id" {
  description = "Cloudflare zone ID of the delegated child zone."
  value       = cloudflare_zone.this.id
}

output "zone_name" {
  description = "Fully-qualified name of the delegated child zone."
  value       = cloudflare_zone.this.name
}

output "name_servers" {
  description = "Cloudflare name servers assigned to the delegated child zone — the delegation target. Hand these to the parent operator when manage_delegation is false."
  value       = cloudflare_zone.this.name_servers
}

output "record_fqdn" {
  description = "Fully-qualified name of the service A record."
  value       = cloudflare_dns_record.this.name
}

output "delegation_managed" {
  description = "Whether this module wrote the NS delegation records into the parent zone."
  value       = var.manage_delegation
}
