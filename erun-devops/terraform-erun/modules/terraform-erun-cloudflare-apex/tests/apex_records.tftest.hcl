mock_provider "cloudflare" {
  mock_data "cloudflare_zone" {
    defaults = {
      zone_id = "zone-abc123"
    }
  }
}

variables {
  cloudflare_account_id = "acct-123"
  base_domain_name      = "erunpaas.com"
  ip_address            = "203.0.113.10"
}

run "apex_record_is_a_concrete_create_action" {
  command = plan

  assert {
    condition     = cloudflare_dns_record.apex.name == "erunpaas.com"
    error_message = "the apex record must target base_domain_name itself, not a subdomain of it"
  }

  assert {
    condition     = cloudflare_dns_record.apex.type == "A"
    error_message = "the apex record must be an A record"
  }

  assert {
    condition     = cloudflare_dns_record.apex.content == "203.0.113.10"
    error_message = "the apex record must resolve to the configured ingress IP"
  }

  assert {
    condition     = cloudflare_dns_record.apex.zone_id == "zone-abc123"
    error_message = "the apex record must be written into the looked-up apex zone"
  }
}

run "www_record_defaults_on" {
  command = plan

  assert {
    condition     = length(cloudflare_dns_record.www) == 1
    error_message = "manage_www must default to true"
  }

  assert {
    condition     = cloudflare_dns_record.www[0].name == "www.erunpaas.com"
    error_message = "the www record must be a label under base_domain_name"
  }
}

run "www_record_can_be_disabled" {
  command = plan

  variables {
    manage_www = false
  }

  assert {
    condition     = length(cloudflare_dns_record.www) == 0
    error_message = "manage_www = false must not create the www record"
  }
}

run "explicit_zone_id_skips_the_lookup" {
  command = plan

  variables {
    parent_zone_id = "zone-explicit"
  }

  assert {
    condition     = length(data.cloudflare_zone.apex) == 0
    error_message = "an explicit parent_zone_id must skip the zone lookup"
  }

  assert {
    condition     = cloudflare_dns_record.apex.zone_id == "zone-explicit"
    error_message = "the explicit parent_zone_id must be used as the zone to write into"
  }
}

run "malformed_base_domain_is_refused" {
  command = plan

  variables {
    base_domain_name = "not a domain"
  }

  expect_failures = [var.base_domain_name]
}

run "malformed_ip_address_is_refused" {
  command = plan

  variables {
    ip_address = "not-an-ip"
  }

  expect_failures = [var.ip_address]
}
