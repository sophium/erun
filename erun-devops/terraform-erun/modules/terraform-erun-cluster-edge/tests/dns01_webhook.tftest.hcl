# Locks the fix for the coupling between the platform Issuer's own DNS-01
# solver (dns01_provider) and whether the per-cluster webhook shim installs.
# Runs entirely against mocked providers: no real cluster is touched.

mock_provider "helm" {}
mock_provider "kubernetes" {}

variables {
  services_zone = "services.example.com"
  acme_email    = "ops@example.com"
}

# The exact shape frs-prod needs: platform Issuer stays on rfc2136, but the
# webhook shim still installs so erun-expose's per-tenant brokered Issuers can
# solve. Deliberately omits dns01_token_secret_name: the shim never reads one,
# and a platform installing it for its tenants has none of its own to name --
# per-env token Secrets land beside each environment. This mirrors the
# documented apply in erun-enable-hosting-edge exactly, so the two cannot
# drift apart again.
run "webhook_shim_installs_while_platform_issuer_stays_on_rfc2136" {
  command = plan

  variables {
    dns01_provider        = "powerdns-rfc2136"
    install_dns01_webhook = true
    powerdns_nameserver   = "erun-powerdns.frs-prod.svc.cluster.local:53"
    rfc2136_tsig_key_name = "erun-tsig"
    rfc2136_tsig_secret   = "c2VjcmV0"
    broker_url            = "https://api.frs-prod.services.example.com/v1/dns01"
    dns01_webhook_image   = "ghcr.io/sophium/erun-dns01-webhook:1.0.0"
  }

  assert {
    condition     = length(helm_release.dns01_webhook) == 1
    error_message = "the webhook shim must install when install_dns01_webhook = true, even though the platform Issuer's own solver is powerdns-rfc2136"
  }

  assert {
    condition     = [for s in helm_release.issuer.set : s.value if s.name == "dns01Provider"][0] == "powerdns-rfc2136"
    error_message = "the platform Issuer must keep solving via rfc2136 when dns01_provider is powerdns-rfc2136, regardless of the webhook shim being installed"
  }
}

# Back-compat: dns01_provider alone still drives the shim when the new
# variable is left unset.
run "webhook_shim_defaults_off_for_plain_rfc2136" {
  command = plan

  variables {
    dns01_provider        = "powerdns-rfc2136"
    powerdns_nameserver   = "erun-powerdns.frs-prod.svc.cluster.local:53"
    rfc2136_tsig_key_name = "erun-tsig"
    rfc2136_tsig_secret   = "c2VjcmV0"
  }

  assert {
    condition     = length(helm_release.dns01_webhook) == 0
    error_message = "install_dns01_webhook must default to false when dns01_provider is not powerdns-broker (back-compat)"
  }
}

run "webhook_shim_defaults_on_for_broker_provider_back_compat" {
  command = plan

  variables {
    dns01_provider          = "powerdns-broker"
    broker_url              = "https://api.example.com/v1/dns01"
    dns01_webhook_image     = "ghcr.io/sophium/erun-dns01-webhook:1.0.0"
    dns01_token_secret_name = "some-env-dns01-token"
  }

  assert {
    condition     = length(helm_release.dns01_webhook) == 1
    error_message = "install_dns01_webhook must default to true when dns01_provider is powerdns-broker (back-compat)"
  }
}

# The combination that produced the undeletable namespace: the platform
# Issuer's own solver is the webhook, but the shim that would serve it is
# explicitly turned off. Must be rejected at plan time, not at challenge time.
run "broker_platform_issuer_without_webhook_shim_is_rejected" {
  command = plan

  variables {
    dns01_provider          = "powerdns-broker"
    install_dns01_webhook   = false
    broker_url              = "https://api.example.com/v1/dns01"
    dns01_webhook_image     = "ghcr.io/sophium/erun-dns01-webhook:1.0.0"
    dns01_token_secret_name = "some-env-dns01-token"
  }

  expect_failures = [helm_release.issuer]
}

# The token secret is required where it is actually read: the platform's own
# Issuer, in broker mode. Guarding it there rather than on the shim is what
# lets the case above omit it.
run "broker_mode_without_a_token_secret_is_rejected" {
  command = plan

  variables {
    dns01_provider      = "powerdns-broker"
    broker_url          = "https://api.frs-prod.services.example.com/v1/dns01"
    dns01_webhook_image = "ghcr.io/sophium/erun-dns01-webhook:1.0.0"
  }

  expect_failures = [helm_release.issuer]
}

# A private shim image needs a pull secret, and nothing renders one unless the
# module threads it. Asserted at the module level because module charts have no
# render harness -- see the PR for why that gap is left alone here.
run "webhook_shim_threads_image_pull_secrets" {
  command = plan

  variables {
    dns01_provider                   = "powerdns-rfc2136"
    install_dns01_webhook            = true
    powerdns_nameserver              = "erun-powerdns.frs-prod.svc.cluster.local:53"
    rfc2136_tsig_key_name            = "erun-tsig"
    rfc2136_tsig_secret              = "c2VjcmV0"
    broker_url                       = "https://api.frs-prod.services.example.com/v1/dns01"
    dns01_webhook_image              = "ghcr.io/sophium/erun-dns01-webhook:1.0.0"
    dns01_webhook_image_pull_secrets = ["ghcr-pull"]
  }

  assert {
    condition     = length([for s in helm_release.dns01_webhook[0].set : s if s.name == "imagePullSecrets[0].name" && s.value == "ghcr-pull"]) == 1
    error_message = "the shim must receive its image pull secrets, or a private image leaves the APIService at MissingEndpoints"
  }
}

# Default stays empty so a public-registry install is unchanged.
run "webhook_shim_sets_no_pull_secrets_by_default" {
  command = plan

  variables {
    dns01_provider        = "powerdns-rfc2136"
    install_dns01_webhook = true
    powerdns_nameserver   = "erun-powerdns.frs-prod.svc.cluster.local:53"
    rfc2136_tsig_key_name = "erun-tsig"
    rfc2136_tsig_secret   = "c2VjcmV0"
    broker_url            = "https://api.frs-prod.services.example.com/v1/dns01"
    dns01_webhook_image   = "ghcr.io/sophium/erun-dns01-webhook:1.0.0"
  }

  assert {
    condition     = length([for s in helm_release.dns01_webhook[0].set : s if strcontains(s.name, "imagePullSecrets")]) == 0
    error_message = "no pull secrets must be set when none are configured"
  }
}
