output "zone_id" {
  description = "Cloudflare zone ID of the apex zone the records were written into."
  value       = local.zone_id
}

output "apex_record_fqdn" {
  description = "Fully-qualified name of the apex A record."
  value       = cloudflare_dns_record.apex.name
}

output "www_record_fqdn" {
  description = "Fully-qualified name of the www A record, or null when manage_www is false."
  value       = var.manage_www ? cloudflare_dns_record.www[0].name : null
}
