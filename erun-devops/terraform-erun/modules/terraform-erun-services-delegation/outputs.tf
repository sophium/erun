output "delegated_zone_name" {
  description = "Fully-qualified name of the delegated child zone."
  value       = local.delegated_zone_name
}

output "nameservers" {
  description = "Nameserver FQDNs the child zone is delegated to."
  value       = var.nameservers
}

output "glue_fqdns" {
  description = "Nameserver FQDNs for which glue A records were published in the parent zone."
  value       = keys(var.glue_records)
}

output "parent_zone_id" {
  description = "Cloudflare zone ID the delegation records were written into."
  value       = local.parent_zone_id
}
