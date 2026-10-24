# Wrappers pass explicit null for "use the default", and Terraform does not
# substitute defaults for caller-assigned nulls. Runs against mocked providers.

mock_provider "helm" {}
mock_provider "kubernetes" {
  # install_coredns_forward reads the Corefile back; it must import the custom dir.
  mock_data "kubernetes_config_map" {
    defaults = {
      data = {
        Corefile = <<-EOT
    .:53 {
        errors
        health
        kubernetes cluster.local in-addr.arpa ip6.arpa
        forward . /etc/resolv.conf
        cache 30
        import /etc/coredns/custom/*.server
    }
        EOT
      }
    }
  }
}

variables {
  services_zone        = "services.example.com"
  acme_email           = "ops@example.com"
  cloudflare_api_token = "test-token"
}

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
    manage_transport_policy          = null
    http01_acme_challenges_present   = null
    acme_challenge_path_exempt       = null
    install_cert_manager             = null
    wildcard_certificate_enabled     = null
    coredns_configmap_name           = null
    manage_coredns_custom_configmap  = null
  }

  assert {
    condition     = can(regex("forward \\. 1\\.1\\.1\\.1 1\\.0\\.0\\.1 8\\.8\\.8\\.8", kubernetes_config_map_v1_data.coredns_forward[0].data["erunpaas-com.server"]))
    error_message = "coredns_forward_upstreams = null must resolve to the module's documented default resolvers, not abort on length(null)"
  }

  # `null != ""` is true, so an unnormalized guard selects null.
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

  # A null operand in the redirect's `&&` would abort the plan.
  assert {
    condition     = output.edge_transport_policy.acme_challenge_path_exempt == false && length(output.edge_transport_policy.redirect_objects) == 0
    error_message = "acme_challenge_path_exempt = null must resolve to the default false"
  }

  assert {
    condition     = length(local.acme_exempt_redirect_objects) == 0
    error_message = "a null HTTP-01 declaration must leave the entrypoint-wide redirect in place"
  }
}

# coalesce() is wrong here: it also skips "" and coalesce("", "") errors.
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

run "an_explicit_false_is_honored_against_a_true_default" {
  command = plan

  variables {
    install_ingress_controller = false
    # install_ingress_controller = false alone is refused while the policy is managed.
    manage_transport_policy = false
  }

  assert {
    condition     = output.ingress_class == null
    error_message = "install_ingress_controller = false must be honored, not replaced by the true default"
  }
}
