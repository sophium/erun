# Locks #1161: a wrapper module cannot conditionally omit an argument in HCL, so
# "use this module's default" has to be expressible as an explicit null. Terraform
# does not substitute a variable's default for a caller-assigned null -- the null
# reaches validation and every use -- so without normalization `length(null)`
# aborts before any plan and `null != ""` is *true*, quietly selecting the wrong
# branch of a guard. Runs entirely against mocked providers: no real cluster.

mock_provider "helm" {}
mock_provider "kubernetes" {}

variables {
  services_zone        = "services.example.com"
  acme_email           = "ops@example.com"
  cloudflare_api_token = "test-token"
}

# The reported reproduction: a wrapper threading its own optional variables
# through passes null for the ones its caller left unset. Every one of these was
# either an abort or a wrong-branch selection before normalization.
run "null_for_every_optional_input_resolves_the_documented_defaults" {
  command = plan

  variables {
    install_coredns_forward          = true
    base_domain_name                 = "erunpaas.com"
    coredns_forward_upstreams        = null
    acme_server                      = null
    namespace                        = null
    ingress_namespace                = null
    issuer_name                      = null
    cert_manager_chart_version       = null
    traefik_chart_version            = null
    dns01_provider                   = null
    dns01_webhook_image              = null
    dns01_webhook_image_pull_secrets = null
    dns01_webhook_group_name         = null
    broker_url                       = null
    dns01_token_secret_name          = null
    powerdns_nameserver              = null
    rfc2136_tsig_key_name            = null
    rfc2136_tsig_algorithm           = null
    rfc2136_tsig_secret              = null
    env_label                        = null
    env_namespace                    = null
    per_env_certificate_enabled      = null
    install_ingress_controller       = null
    install_cert_manager             = null
    wildcard_certificate_enabled     = null
  }

  # The originally-reported abort: length(null) on the upstreams list.
  assert {
    condition     = can(regex("forward \\. 1\\.1\\.1\\.1 1\\.0\\.0\\.1 8\\.8\\.8\\.8", kubernetes_config_map.coredns_forward[0].data["erunpaas-com.server"]))
    error_message = "coredns_forward_upstreams = null must resolve to the module's documented default resolvers, not abort on length(null)"
  }

  # The trap that cost a release: `null != ""` is true, so the guard selected
  # null as the image and left the helm release with a valueless set block.
  assert {
    condition     = can(regex("^ghcr\\.io/sophium/erun-dns01-webhook:", local.dns01_webhook_image))
    error_message = "dns01_webhook_image = null must fall back to the bundled chart's own release, not be selected as the image"
  }

  assert {
    condition     = local.arg_namespace == "cert-manager"
    error_message = "namespace = null must resolve to the documented default"
  }

  assert {
    condition     = local.arg_issuer_name == "erun-cloudflare"
    error_message = "issuer_name = null must resolve to the documented default"
  }

  assert {
    condition     = local.arg_dns01_provider == "cloudflare"
    error_message = "dns01_provider = null must resolve to the documented default rather than failing its contains() validation"
  }

  assert {
    condition     = length(local.arg_dns01_webhook_image_pull_secrets) == 0
    error_message = "a null list input must resolve to the empty default"
  }

  assert {
    condition     = output.ingress_class == "traefik"
    error_message = "install_ingress_controller = null must resolve to the default true, not a null condition"
  }
}

# The normalization must test for null specifically. coalesce() is the obvious
# reach and is wrong twice over: it skips empty strings as well as nulls, and
# coalesce("", "") is a hard error -- so an optional string that is legitimately
# empty would either be silently replaced by the default or abort the apply.
run "an_explicitly_empty_input_is_not_replaced_by_the_default" {
  command = plan

  variables {
    broker_url              = ""
    dns01_token_secret_name = ""
    powerdns_nameserver     = ""
  }

  assert {
    condition     = local.arg_broker_url == ""
    error_message = "an empty optional string must stay empty: coalesce() would abort here, since all its arguments would be empty"
  }
}

# The mirror for booleans: an explicit false against a true default must be
# honored, not promoted to the default.
run "an_explicit_false_is_honored_against_a_true_default" {
  command = plan

  variables {
    install_ingress_controller = false
  }

  assert {
    condition     = output.ingress_class == null
    error_message = "install_ingress_controller = false must be honored, not replaced by the true default"
  }
}
