# Locks the fix for a cluster that cannot resolve the platform's own published
# names because k3s's CoreDNS falls through to the node's resolver for
# anything outside cluster.local: install_coredns_forward declares a CoreDNS
# custom server block for base_domain_name instead of leaving that resolution
# to whatever the node happens to do. Runs entirely against mocked providers:
# no real cluster is touched.

mock_provider "helm" {}

mock_provider "kubernetes" {
  # The forward only works if CoreDNS's Corefile imports the custom directory,
  # so the module reads the Corefile back and refuses to apply otherwise. The
  # default mock is a k3s-shaped Corefile that does import it.
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

# Adding this module version to an already-applied cluster must not change its
# DNS behavior until the operator opts in.
run "coredns_forward_disabled_by_default_preserves_existing_clusters" {
  command = plan

  assert {
    condition     = length(kubernetes_config_map_v1_data.coredns_forward) == 0
    error_message = "install_coredns_forward must default to false so an existing cluster's DNS behavior does not change on a module upgrade"
  }

  assert {
    condition     = length(kubernetes_config_map.coredns_custom) == 0
    error_message = "the shared coredns-custom ConfigMap must not be created until the forward is enabled"
  }
}

run "coredns_forward_installs_the_custom_server_block" {
  command = plan

  variables {
    install_coredns_forward = true
    base_domain_name        = "erunpaas.com"
  }

  assert {
    condition     = length(kubernetes_config_map_v1_data.coredns_forward) == 1
    error_message = "install_coredns_forward = true must manage the coredns-custom key"
  }

  assert {
    condition     = kubernetes_config_map_v1_data.coredns_forward[0].metadata[0].name == "coredns-custom"
    error_message = "must target k3s's optional coredns-custom ConfigMap by name"
  }

  assert {
    condition     = kubernetes_config_map_v1_data.coredns_forward[0].metadata[0].namespace == "kube-system"
    error_message = "coredns-custom must live in kube-system, where k3s's CoreDNS mounts it"
  }

  assert {
    condition     = contains(keys(kubernetes_config_map_v1_data.coredns_forward[0].data), "erunpaas-com.server")
    error_message = "the server-block file must be named after base_domain_name so CoreDNS's *.server import picks it up"
  }

  assert {
    condition     = can(regex("erunpaas\\.com:53", kubernetes_config_map_v1_data.coredns_forward[0].data["erunpaas-com.server"]))
    error_message = "the server block must declare a zone for base_domain_name"
  }

  assert {
    condition     = can(regex("forward \\. 1\\.1\\.1\\.1 1\\.0\\.0\\.1 8\\.8\\.8\\.8", kubernetes_config_map_v1_data.coredns_forward[0].data["erunpaas-com.server"]))
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
    condition     = can(regex("forward \\. 10\\.0\\.0\\.53 10\\.0\\.0\\.54", kubernetes_config_map_v1_data.coredns_forward[0].data["erunpaas-com.server"]))
    error_message = "coredns_forward_upstreams must control the forward target list"
  }
}

run "coredns_forward_requires_base_domain_name" {
  command = plan

  variables {
    install_coredns_forward = true
  }

  expect_failures = [kubernetes_config_map.coredns_custom]
}

# ---------------------------------------------------------------------------
# #1165: the three findings below are what this resource got wrong.
# ---------------------------------------------------------------------------

# Finding 1. Entries go straight into a `forward .` directive. A malformed one
# writes an invalid server block, and CoreDNS keeps serving from its loaded
# config -- so nothing looks wrong until its next restart, when the cluster has
# no DNS at all and the cause is an apply from days earlier.
run "malformed_upstreams_are_refused_at_apply_time" {
  command = plan

  variables {
    install_coredns_forward   = true
    base_domain_name          = "erunpaas.com"
    coredns_forward_upstreams = ["1.1.1.1", "not a resolver"]
  }

  expect_failures = [var.coredns_forward_upstreams]
}

run "a_stray_comma_in_an_upstream_is_refused" {
  command = plan

  variables {
    install_coredns_forward   = true
    base_domain_name          = "erunpaas.com"
    coredns_forward_upstreams = ["1.1.1.1,1.0.0.1"]
  }

  expect_failures = [var.coredns_forward_upstreams]
}

run "legitimate_upstream_forms_are_accepted" {
  command = plan

  variables {
    install_coredns_forward = true
    base_domain_name        = "erunpaas.com"
    coredns_forward_upstreams = [
      "10.0.0.53",
      "10.0.0.54:5353",
      "[2606:4700:4700::1111]:53",
      "dns.example.com",
      "/etc/resolv.conf",
    ]
  }

  assert {
    condition     = can(regex("forward \\. 10\\.0\\.0\\.53 10\\.0\\.0\\.54:5353", kubernetes_config_map_v1_data.coredns_forward[0].data["erunpaas-com.server"]))
    error_message = "the validation must not reject the forms CoreDNS actually accepts"
  }
}

# Finding 1, lower-severity half: a server block for a cluster-internal zone
# would shadow CoreDNS's kubernetes plugin and kill .svc.cluster.local
# resolution for the whole cluster. The domain regex happily accepts it.
run "a_cluster_internal_base_domain_is_refused" {
  command = plan

  variables {
    install_coredns_forward = true
    base_domain_name        = "cluster.local"
  }

  expect_failures = [var.base_domain_name]
}

# Finding 3. The whole mechanism depends on the Corefile importing the custom
# directory. Without it the ConfigMap applies cleanly, the module reports
# success, and in-cluster resolution is completely unchanged -- a false success
# that goes unnoticed until a certificate issuance or renewal fails.
run "a_corefile_without_the_custom_import_is_refused" {
  command = plan

  variables {
    install_coredns_forward = true
    base_domain_name        = "erunpaas.com"
  }

  override_data {
    target = data.kubernetes_config_map.coredns[0]
    values = {
      data = {
        Corefile = <<-EOT
          .:53 {
              kubernetes cluster.local in-addr.arpa ip6.arpa
              forward . /etc/resolv.conf
          }
        EOT
      }
    }
  }

  expect_failures = [kubernetes_config_map_v1_data.coredns_forward[0]]
}

# Finding 2. coredns-custom is a shared extension point. An operator whose
# cluster already has one -- with keys this module knows nothing about -- must
# be able to keep the module out of the object's lifecycle entirely, while it
# still manages its own key.
run "the_shared_configmap_lifecycle_can_be_left_to_someone_else" {
  command = plan

  variables {
    install_coredns_forward         = true
    base_domain_name                = "erunpaas.com"
    manage_coredns_custom_configmap = false
  }

  assert {
    condition     = length(kubernetes_config_map.coredns_custom) == 0
    error_message = "manage_coredns_custom_configmap = false must not create or own the shared ConfigMap"
  }

  assert {
    condition     = length(kubernetes_config_map_v1_data.coredns_forward) == 1
    error_message = "the module must still manage its own key when it does not own the object"
  }
}
