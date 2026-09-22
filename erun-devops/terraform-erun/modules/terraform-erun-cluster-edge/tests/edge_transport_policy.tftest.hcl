# Locks #2252: the public front door served plaintext. Traefik answers :80 for
# every host it routes, so whether a visitor who types the bare domain -- or
# follows a relative redirect issued behind the edge, as the IdP's own login
# chain does -- is upgraded to https is a property of the entrypoint, not of
# each Ingress. These runs pin that policy, its opt-outs, and the HSTS
# middleware that has to exist for the header to be served at all.
#
# Runs entirely against mocked providers: no real cluster.

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
      "--entryPoints.web.http.redirections.entryPoint.to=websecure",
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

  # The header is only served because this object exists: an entrypoint naming
  # a middleware that was never created is reported by Traefik and the response
  # goes out without it, which is indistinguishable from the bug.
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

# HSTS is a commitment a browser cannot be talked out of early, so the shipped
# default is the conservative one: one day, no includeSubDomains, no preload.
# Raising it is a deliberate edit, not a drift.
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

# A platform that has verified every name under the domain raises the
# commitment explicitly; the switches have to reach the rendered middleware.
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

# An edge fronting a host that is deliberately plaintext turns each half off
# independently: the redirect and the header are separate decisions.
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

# A cluster that already runs an ingress controller gets no edge from this
# module at all -- and therefore no transport policy either: the controller it
# already has owns that. Saying so is now required rather than assumed, because
# the plan that assumed it applied cleanly while every public host served
# cleartext.
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

# The reported defect: with no controller installed and the policy left
# managed, every transport switch is accepted, defaulted, documented -- and
# dropped. The plan applied cleanly and reported success while
# http://<any exposed host> answered 200 with no redirect and no
# Strict-Transport-Security, which is exactly the symptom the switches exist to
# prevent. This run fails without the output's precondition.
run "installing_no_controller_while_still_managing_the_policy_is_refused" {
  command = plan

  variables {
    install_ingress_controller = false
  }

  expect_failures = [output.edge_transport_policy]
}

# Refusal is not the only way out, and it is not the one a bring-your-own
# cluster wants: the policy is exposed as data, so the controller that is
# already there can carry it without a hand-rolled overlay that duplicates the
# module's own defaults and then drifts from them.
run "the_policy_is_exposed_for_a_controller_this_module_does_not_install" {
  command = plan

  variables {
    install_ingress_controller = false
    manage_transport_policy    = false
  }

  assert {
    condition = toset(output.edge_transport_policy.traefik_additional_arguments) == toset([
      "--entryPoints.web.http.redirections.entryPoint.to=websecure",
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

# The switch is about who applies the policy, not about the controller: a
# platform that wants Traefik installed but keeps its transport policy (and the
# HSTS commitment a browser cannot be talked out of) in its own hands gets a
# controller with the arguments left off.
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
