# Plaintext redirect and HSTS are entrypoint properties, not per-Ingress ones.
# Runs against mocked providers.

mock_provider "helm" {}
mock_provider "kubernetes" {}

variables {
  services_zone        = "services.example.com"
  acme_email           = "ops@example.com"
  cloudflare_api_token = "test-token"
}

run "plaintext_redirects_to_https_and_hsts_is_served_by_default" {
  command = plan

  assert {
    condition = toset([for s in helm_release.traefik[0].set : s.value]) == toset([
      "--entryPoints.web.http.redirections.entryPoint.to=:443",
      "--entryPoints.web.http.redirections.entryPoint.scheme=https",
      "--entryPoints.web.http.redirections.entryPoint.permanent=true",
      "--entryPoints.websecure.http.middlewares=${local.hsts_middleware_ref}",
    ])
    error_message = "the edge must redirect its plaintext entrypoint to https and apply the HSTS middleware on the secure one"
  }

  assert {
    condition     = local.hsts_middleware_ref == "traefik-erun-edge-hsts@kubernetescrd"
    error_message = "the HSTS middleware reference must resolve to the ingress namespace's own namespace/name, not a literal"
  }

  # Traefik serves responses without a middleware the entrypoint names but
  # that was never created.
  assert {
    condition = anytrue([
      for v in helm_release.traefik[0].values :
      strcontains(v, "erun-edge-hsts") && strcontains(v, "Middleware")
    ])
    error_message = "the HSTS Middleware must ride in the same release as the controller that names it"
  }

  assert {
    condition = anytrue([
      for v in helm_release.traefik[0].values :
      strcontains(v, "stsSeconds") && strcontains(v, "forceSTSHeader")
    ])
    error_message = "the HSTS Middleware must set stsSeconds and force the header on https responses"
  }
}

# Traefik resolves a named redirect target to that entrypoint's listen port, and
# the chart listens on :8443 behind a Service that publishes 443, so naming
# websecure sends every plaintext visitor to https://<host>:8443/.
run "the_plaintext_redirect_lands_on_the_public_https_port" {
  command = plan

  assert {
    condition     = [for s in helm_release.traefik[0].set : s.value if strcontains(s.value, "redirections.entryPoint.to=")] == ["--entryPoints.web.http.redirections.entryPoint.to=:443"]
    error_message = "the plaintext redirect must target the published port :443, which Traefik leaves out of the Location; a named entrypoint (websecure) resolves to its :8443 listen port, which nothing outside the pod reaches"
  }

  assert {
    condition     = contains(output.edge_transport_policy.traefik_additional_arguments, "--entryPoints.web.http.redirections.entryPoint.to=:443")
    error_message = "a bring-your-own controller is handed the same redirect target, since the upstream Traefik chart (which k3s also bundles) listens on :8443 behind 443 there too"
  }
}

# A browser cannot be talked out of HSTS early, so the default stays conservative.
run "hsts_defaults_stay_conservative" {
  command = plan

  assert {
    condition = anytrue([
      for v in helm_release.traefik[0].values :
      strcontains(v, "86400")
    ])
    error_message = "the default HSTS max-age must be one day (86400 seconds)"
  }

  assert {
    condition = alltrue([
      for v in helm_release.traefik[0].values :
      strcontains(v, "\"stsIncludeSubdomains\": false") && strcontains(v, "\"stsPreload\": false")
    ])
    error_message = "includeSubDomains and preload must stay off by default: they bind names this module does not serve"
  }
}

run "hsts_commitment_can_be_raised" {
  command = plan

  variables {
    hsts_max_age_seconds    = 31536000
    hsts_include_subdomains = true
    hsts_preload            = true
  }

  assert {
    condition = anytrue([
      for v in helm_release.traefik[0].values :
      strcontains(v, "31536000") && strcontains(v, "\"stsIncludeSubdomains\": true") && strcontains(v, "\"stsPreload\": true")
    ])
    error_message = "hsts_max_age_seconds, includeSubdomains and preload must reach the rendered Middleware"
  }
}

run "redirect_and_hsts_are_independently_switchable" {
  command = plan

  variables {
    http_redirect_enabled = false
  }

  assert {
    condition     = alltrue([for s in helm_release.traefik[0].set : !strcontains(s.value, "redirections.entryPoint")])
    error_message = "http_redirect_enabled = false must render no redirect arguments"
  }

  assert {
    condition     = anytrue([for s in helm_release.traefik[0].set : strcontains(s.value, "websecure.http.middlewares")])
    error_message = "turning the redirect off must not also drop HSTS"
  }
}

run "hsts_can_be_turned_off_without_dropping_the_redirect" {
  command = plan

  variables {
    hsts_enabled = false
  }

  assert {
    condition     = alltrue([for s in helm_release.traefik[0].set : !strcontains(s.value, "middlewares")])
    error_message = "hsts_enabled = false must render no middleware reference"
  }

  assert {
    condition     = length(helm_release.traefik[0].values) == 0
    error_message = "hsts_enabled = false must not create the Middleware object"
  }

  assert {
    condition     = length([for s in helm_release.traefik[0].set : s.value if strcontains(s.value, "permanent=true")]) == 1
    error_message = "turning HSTS off must not also drop the plaintext redirect"
  }
}

run "an_existing_ingress_controller_is_left_alone" {
  command = plan

  variables {
    install_ingress_controller = false
    manage_transport_policy    = false
  }

  assert {
    condition     = length(helm_release.traefik) == 0
    error_message = "install_ingress_controller = false must not install or configure a controller"
  }

  assert {
    condition     = length(output.edge_transport_policy.traefik_additional_arguments) == 4
    error_message = "installing no controller must not cost the caller the policy: it is still exposed at edge_transport_policy"
  }
}

run "installing_no_controller_while_still_managing_the_policy_is_refused" {
  command = plan

  variables {
    install_ingress_controller = false
  }

  expect_failures = [output.edge_transport_policy]
}

run "the_policy_is_exposed_for_a_controller_this_module_does_not_install" {
  command = plan

  variables {
    install_ingress_controller = false
    manage_transport_policy    = false
  }

  assert {
    condition = toset(output.edge_transport_policy.traefik_additional_arguments) == toset([
      "--entryPoints.web.http.redirections.entryPoint.to=:443",
      "--entryPoints.web.http.redirections.entryPoint.scheme=https",
      "--entryPoints.web.http.redirections.entryPoint.permanent=true",
      "--entryPoints.websecure.http.middlewares=${local.hsts_middleware_ref}",
    ])
    error_message = "a cluster managing the policy itself must be handed the same arguments this module would have installed"
  }

  assert {
    condition     = output.edge_transport_policy.hsts_middleware.spec.headers.stsSeconds == 86400
    error_message = "the exposed policy must carry the HSTS commitment, not just the redirect"
  }

  assert {
    condition     = output.edge_transport_policy.manager == "caller"
    error_message = "the resolved policy must say which side carries it"
  }
}

# Traefik applies the entrypoint-wide redirect to /.well-known/acme-challenge/
# too, which starves an HTTP-01 solver.
run "the_acme_challenge_path_can_be_exempted_from_the_redirect" {
  command = plan

  variables {
    acme_challenge_path_exempt = true
  }

  # Both forms at once would race, so the blanket one must be absent.
  assert {
    condition     = alltrue([for s in helm_release.traefik[0].set : !strcontains(s.value, "redirections.entryPoint")])
    error_message = "the exempting form must drop the entrypoint-wide redirect entirely, not emit both"
  }

  assert {
    condition = anytrue([
      for v in helm_release.traefik[0].values :
      strcontains(v, "IngressRoute") && strcontains(v, "!PathPrefix(`/.well-known/acme-challenge/`)")
    ])
    error_message = "the redirect must ride on a router whose rule excludes the ACME challenge prefix; a Middleware alone carries no rule and would still catch the solver"
  }

  assert {
    condition = anytrue([
      for v in helm_release.traefik[0].values :
      strcontains(v, "redirectScheme") && strcontains(v, "erun-edge-http-redirect")
    ])
    error_message = "the exempting router must name a redirect Middleware that exists in the same release"
  }

  assert {
    condition     = anytrue([for s in helm_release.traefik[0].set : strcontains(s.value, "websecure.http.middlewares")])
    error_message = "exempting the challenge path from the redirect must not also drop HSTS"
  }
}

# Traefik derives router priority from rule length, so without an explicit
# priority a longer Host() rule outranks the catch-all and skips the redirect.
run "the_exempting_router_outranks_a_longer_host_rule" {
  command = plan

  variables {
    acme_challenge_path_exempt = true
  }

  assert {
    condition = alltrue([
      for r in local.acme_exempt_redirect_router.spec.routes :
      # Traefik's effective priority: declared, else the rule's length.
      try(r.priority, length(r.match)) > length("Host(`console.erunpaas.com`) && PathPrefix(`/`)")
    ])
    error_message = "the ACME-exempting catch-all must declare a priority that outranks the host routers it has to redirect; on Traefik's derived default (the rule's own length, 43 here) it loses to any Host(...) rule longer than that, and the redirect silently never applies to those hosts"
  }

  # Safe above every host router only because the rule excludes the challenge path.
  assert {
    condition = alltrue([
      for r in local.acme_exempt_redirect_router.spec.routes :
      strcontains(r.match, "!PathPrefix")
    ])
    error_message = "a priority above every host router is only safe because the rule is negated: it must not match the challenge path"
  }
}

run "the_blanket_redirect_carries_no_exemption_until_opted_in" {
  command = plan

  assert {
    condition     = length(output.edge_transport_policy.redirect_objects) == 0
    error_message = "the default must render no ACME exemption"
  }

  assert {
    condition     = output.edge_transport_policy.acme_challenge_path_exempt == false
    error_message = "the exposed policy must say the challenge path is not exempt, so a caller reading it is not told otherwise"
  }

  assert {
    condition     = output.edge_transport_policy.http01_acme_challenges_present == false
    error_message = "the exposed policy must echo the HTTP-01 declaration as resolved; a caller that never sees this module's resources has no other way to read back the fact the policy was built on"
  }

  assert {
    condition     = length([for s in helm_release.traefik[0].set : s.value if strcontains(s.value, "redirections.entryPoint.to=")]) == 1
    error_message = "the default must stay the entrypoint-wide redirect that already covers /.well-known/acme-challenge/"
  }
}

run "an_http01_issuer_without_the_exemption_is_refused" {
  command = plan

  variables {
    http01_acme_challenges_present = true
  }

  expect_failures = [output.edge_transport_policy]
}

run "an_http01_issuer_with_no_redirect_at_all_is_not_refused" {
  command = plan

  variables {
    http01_acme_challenges_present = true
    http_redirect_enabled          = false
  }

  assert {
    condition     = length(output.edge_transport_policy.redirect_objects) == 0
    error_message = "with the redirect off there is no redirect for the challenge path to be exempt from"
  }
}

run "the_exemption_is_exported_for_a_controller_this_module_does_not_install" {
  command = plan

  variables {
    install_ingress_controller     = false
    manage_transport_policy        = false
    http01_acme_challenges_present = true
    acme_challenge_path_exempt     = true
  }

  assert {
    condition     = length(output.edge_transport_policy.redirect_objects) == 2
    error_message = "a bring-your-own controller must be handed both halves: the Middleware and the router that narrows it"
  }

  assert {
    condition     = output.edge_transport_policy.http01_acme_challenges_present == true
    error_message = "the exposed policy must echo the HTTP-01 declaration it was resolved against, not just the objects that resulted from it"
  }

  assert {
    condition = alltrue([
      for o in output.edge_transport_policy.redirect_objects :
      strcontains(jsonencode(o), "IngressRoute") ? strcontains(jsonencode(o), "1000000") : true
    ])
    error_message = "the exported router must carry its explicit priority into the caller's cluster -- the caller applies this object verbatim, so a priority that only exists in the release this module installs would leave bring-your-own-controller clusters with the same silently ineffective redirect"
  }

  assert {
    condition     = length([for a in output.edge_transport_policy.traefik_additional_arguments : a if strcontains(a, "redirections.entryPoint")]) == 0
    error_message = "the exported policy must not still carry the entrypoint-wide redirect once the challenge path is exempt"
  }

  assert {
    condition     = anytrue([for o in output.edge_transport_policy.redirect_objects : strcontains(jsonencode(o), "acme-challenge")])
    error_message = "the exported router must carry the challenge prefix exclusion, not just a scheme redirect"
  }

  assert {
    condition     = output.edge_transport_policy.hsts_middleware.spec.headers.stsSeconds == 86400
    error_message = "exempting the challenge path from the redirect must not cost the caller HSTS"
  }
}

run "the_exempting_objects_survive_hsts_being_turned_off" {
  command = plan

  variables {
    acme_challenge_path_exempt = true
    hsts_enabled               = false
  }

  assert {
    condition     = length(helm_release.traefik[0].values) == 1
    error_message = "turning HSTS off must not take the exempting redirect objects with it"
  }

  assert {
    condition     = anytrue([for v in helm_release.traefik[0].values : strcontains(v, "IngressRoute")])
    error_message = "the exempting router must still ride in the release when no HSTS Middleware does"
  }
}

run "managing_no_policy_installs_the_controller_without_transport_arguments" {
  command = plan

  variables {
    manage_transport_policy = false
  }

  assert {
    condition     = length(helm_release.traefik) == 1
    error_message = "manage_transport_policy = false must not change whether the controller is installed"
  }

  assert {
    condition     = length(helm_release.traefik[0].set) == 0
    error_message = "manage_transport_policy = false must render no transport arguments"
  }

  assert {
    condition     = length(helm_release.traefik[0].values) == 0
    error_message = "manage_transport_policy = false must not create the HSTS Middleware object"
  }

  assert {
    condition     = output.edge_transport_policy.hsts_middleware.spec.headers.forceSTSHeader
    error_message = "the policy stays exposed even when this module does not carry it"
  }
}
