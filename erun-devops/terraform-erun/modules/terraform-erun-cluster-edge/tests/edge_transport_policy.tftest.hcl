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
# already has owns that, so claiming otherwise here would be a silent no-op.
run "an_existing_ingress_controller_is_left_alone" {
  command = plan

  variables {
    install_ingress_controller = false
  }

  assert {
    condition     = length(helm_release.traefik) == 0
    error_message = "install_ingress_controller = false must not install or configure a controller"
  }
}
