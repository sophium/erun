# Locks the fix for a cluster that cannot resolve the platform's own published
# names because k3s's CoreDNS falls through to the node's resolver for
# anything outside cluster.local: install_coredns_forward declares a CoreDNS
# custom server block for base_domain_name instead of leaving that resolution
# to whatever the node happens to do. Runs entirely against mocked providers:
# no real cluster is touched.

mock_provider "helm" {}
mock_provider "kubernetes" {}

variables {
  services_zone        = "services.example.com"
  acme_email           = "ops@example.com"
  cloudflare_api_token = "test-token"
}

# Adding this module version to an already-applied cluster must not change its
# DNS behavior until the operator opts in.
run "coredns_forward_disabled_by_default_preserves_existing_clusters" {
  command = plan

  assert {
    condition     = length(kubernetes_config_map.coredns_forward) == 0
    error_message = "install_coredns_forward must default to false so an existing cluster's DNS behavior does not change on a module upgrade"
  }
}

run "coredns_forward_installs_the_custom_server_block" {
  command = plan

  variables {
    install_coredns_forward = true
    base_domain_name        = "erunpaas.com"
  }

  assert {
    condition     = length(kubernetes_config_map.coredns_forward) == 1
    error_message = "install_coredns_forward = true must create the coredns-custom ConfigMap entry"
  }

  assert {
    condition     = kubernetes_config_map.coredns_forward[0].metadata[0].name == "coredns-custom"
    error_message = "must target k3s's optional coredns-custom ConfigMap by name"
  }

  assert {
    condition     = kubernetes_config_map.coredns_forward[0].metadata[0].namespace == "kube-system"
    error_message = "coredns-custom must live in kube-system, where k3s's CoreDNS mounts it"
  }

  assert {
    condition     = contains(keys(kubernetes_config_map.coredns_forward[0].data), "erunpaas-com.server")
    error_message = "the server-block file must be named after base_domain_name so CoreDNS's *.server import picks it up"
  }

  assert {
    condition     = can(regex("erunpaas\\.com:53", kubernetes_config_map.coredns_forward[0].data["erunpaas-com.server"]))
    error_message = "the server block must declare a zone for base_domain_name"
  }

  assert {
    condition     = can(regex("forward \\. 1\\.1\\.1\\.1 1\\.0\\.0\\.1 8\\.8\\.8\\.8", kubernetes_config_map.coredns_forward[0].data["erunpaas-com.server"]))
    error_message = "coredns_forward_upstreams must default to public resolvers matching the documented workaround"
  }
}

# An air-gapped or policy-constrained cluster must be able to point the forward
# at its own resolvers instead of the public default.
run "coredns_forward_upstreams_are_configurable" {
  command = plan

  variables {
    install_coredns_forward   = true
    base_domain_name          = "erunpaas.com"
    coredns_forward_upstreams = ["10.0.0.53", "10.0.0.54"]
  }

  assert {
    condition     = can(regex("forward \\. 10\\.0\\.0\\.53 10\\.0\\.0\\.54", kubernetes_config_map.coredns_forward[0].data["erunpaas-com.server"]))
    error_message = "coredns_forward_upstreams must control the forward target list"
  }
}

run "coredns_forward_requires_base_domain_name" {
  command = plan

  variables {
    install_coredns_forward = true
  }

  expect_failures = [kubernetes_config_map.coredns_forward]
}
