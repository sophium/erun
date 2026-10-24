# The local-path helper pod is pinned by spec.nodeName, so only kubelet admits
# it; under DiskPressure kubelet rejects and evicts any non-critical pod. The fix
# is a critical priority class, not a toleration. Runs against mocked providers.

mock_provider "helm" {}

mock_provider "kubernetes" {
  # The provisioner ConfigMap as k3s ships it: no scheduling fields at all.
  mock_data "kubernetes_config_map" {
    defaults = {
      data = {
        "config.json"    = "{\"nodePathMap\":[{\"node\":\"DEFAULT_PATH_FOR_NON_LISTED_NODES\",\"paths\":[\"/var/lib/rancher/k3s/storage\"]}]}"
        "helperPod.yaml" = <<-EOT
          apiVersion: v1
          kind: Pod
          metadata:
            name: helper-pod
          spec:
            containers:
            - name: helper-pod
              image: rancher/mirrored-library-busybox:1.37.0
              imagePullPolicy: IfNotPresent
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

run "helper_pod_resilience_is_off_by_default" {
  command = plan

  assert {
    condition     = length(kubernetes_config_map_v1_data.local_path_helper_pod) == 0
    error_message = "install_local_path_helper_pod_resilience must default to false so a module upgrade does not rewrite a running cluster's provisioner configuration"
  }

  assert {
    condition     = length(data.kubernetes_config_map.local_path) == 0
    error_message = "the provisioner's ConfigMap must not even be read until the operator opts in"
  }

  assert {
    condition     = length(kubernetes_annotations.local_path_provisioner_rollout) == 0
    error_message = "the provisioner Deployment must not be annotated, and so must not roll, until the operator opts in"
  }
}

run "the_helper_pod_is_critical_while_the_node_is_under_disk_pressure" {
  command = plan

  variables {
    install_local_path_helper_pod_resilience = true
  }

  assert {
    condition     = length(kubernetes_config_map_v1_data.local_path_helper_pod) == 1
    error_message = "enabling the feature must manage the provisioner's helperPod.yaml key"
  }

  assert {
    condition     = kubernetes_config_map_v1_data.local_path_helper_pod[0].metadata[0].name == "local-path-config"
    error_message = "must target the local-path provisioner's own ConfigMap by name"
  }

  assert {
    condition     = kubernetes_config_map_v1_data.local_path_helper_pod[0].metadata[0].namespace == "kube-system"
    error_message = "the provisioner's ConfigMap lives in kube-system"
  }

  assert {
    condition     = try(yamldecode(kubernetes_config_map_v1_data.local_path_helper_pod[0].data["helperPod.yaml"]).spec.priorityClassName, "") == "system-node-critical"
    error_message = "the helper pod must carry system-node-critical. Without a priorityClassName its spec.priority resolves to 0, kubelet's eviction manager admits it only while the node has no conditions and evicts it first when the node has any, and this pod is pinned straight onto the pressured node by the provisioner -- so neither provisioning nor reclamation can complete there and a deleted PVC frees no bytes"
  }
}

# Declaring any tolerations suppresses the provisioner's own disk-pressure default.
run "the_disk_pressure_toleration_is_stated_because_declaring_one_suppresses_the_default" {
  command = plan

  variables {
    install_local_path_helper_pod_resilience = true
  }

  assert {
    condition = length([
      for t in try(yamldecode(kubernetes_config_map_v1_data.local_path_helper_pod[0].data["helperPod.yaml"]).spec.tolerations, []) :
      t if try(t.key, "") == "node.kubernetes.io/disk-pressure" && try(t.operator, "") == "Exists" && try(t.effect, "") == "NoSchedule"
    ]) == 1
    error_message = "the merged helper pod template must declare node.kubernetes.io/disk-pressure with operator Exists and effect NoSchedule. The provisioner only appends that toleration itself while the template declares none, so writing a template that declares tolerations without this one turns on this feature and strips the pod of the toleration it already had"
  }
}

run "tolerations_the_distribution_already_set_are_preserved" {
  command = plan

  variables {
    install_local_path_helper_pod_resilience = true
  }

  override_data {
    target = data.kubernetes_config_map.local_path[0]
    values = {
      data = {
        "helperPod.yaml" = <<-EOT
          apiVersion: v1
          kind: Pod
          metadata:
            name: helper-pod
          spec:
            tolerations:
            - key: node-role.kubernetes.io/control-plane
              operator: Exists
              effect: NoSchedule
            containers:
            - name: helper-pod
              image: rancher/mirrored-library-busybox:1.37.0
        EOT
      }
    }
  }

  assert {
    condition = length([
      for t in try(yamldecode(kubernetes_config_map_v1_data.local_path_helper_pod[0].data["helperPod.yaml"]).spec.tolerations, []) :
      t if try(t.key, "") == "node-role.kubernetes.io/control-plane"
    ]) == 1
    error_message = "a toleration the distribution already set must be preserved, not replaced by this module's"
  }

  assert {
    condition = length([
      for t in try(yamldecode(kubernetes_config_map_v1_data.local_path_helper_pod[0].data["helperPod.yaml"]).spec.tolerations, []) :
      t if try(t.key, "") == "node.kubernetes.io/disk-pressure"
    ]) == 1
    error_message = "the requested disk-pressure toleration must be added alongside it"
  }
}

# A mirrored or air-gapped cluster may have chosen a different helper image.
run "the_distributions_own_template_survives_the_merge" {
  command = plan

  variables {
    install_local_path_helper_pod_resilience = true
  }

  assert {
    condition     = try(yamldecode(kubernetes_config_map_v1_data.local_path_helper_pod[0].data["helperPod.yaml"]).spec.containers[0].image, "") == "rancher/mirrored-library-busybox:1.37.0"
    error_message = "the distribution's own helper image must survive the merge untouched"
  }

  assert {
    condition     = try(yamldecode(kubernetes_config_map_v1_data.local_path_helper_pod[0].data["helperPod.yaml"]).metadata.name, "") == "helper-pod"
    error_message = "the rest of the distribution's template must survive the merge untouched"
  }
}

# The provisioner reads the template only at startup on k3s (no
# CONFIG_MOUNT_PATH), so a ConfigMap write alone changes nothing.
run "the_provisioner_is_restarted_so_the_written_template_is_the_one_it_reads" {
  command = plan

  variables {
    install_local_path_helper_pod_resilience = true
  }

  assert {
    condition     = length(kubernetes_annotations.local_path_provisioner_rollout) == 1
    error_message = "the provisioner's pod template must be annotated so the corrected helper pod spec is actually read; without it the apply succeeds and the helper pod stays exactly as inadmissible as before"
  }

  assert {
    condition     = kubernetes_annotations.local_path_provisioner_rollout[0].metadata[0].name == "local-path-provisioner"
    error_message = "the rollout must target the local-path provisioner's own Deployment"
  }

  # A hardcoded digest would never roll the provisioner on a real edit.
  assert {
    condition     = kubernetes_annotations.local_path_provisioner_rollout[0].template_annotations["erun.io/helper-pod-template-digest"] == sha256(kubernetes_config_map_v1_data.local_path_helper_pod[0].data["helperPod.yaml"])
    error_message = "the rollout annotation must be a digest of the helper pod template this apply writes, so that changing that template is what causes the restart"
  }
}

run "the_configmap_name_is_configurable" {
  command = plan

  variables {
    install_local_path_helper_pod_resilience = true
    local_path_configmap_name                = "custom-local-path-config"
    local_path_provisioner_deployment_name   = "custom-local-path-provisioner"
  }

  assert {
    condition     = kubernetes_config_map_v1_data.local_path_helper_pod[0].metadata[0].name == "custom-local-path-config"
    error_message = "local_path_configmap_name must control which ConfigMap is read and written"
  }

  assert {
    condition     = kubernetes_annotations.local_path_provisioner_rollout[0].metadata[0].name == "custom-local-path-provisioner"
    error_message = "local_path_provisioner_deployment_name must control which Deployment is rolled"
  }
}

# A provisioner without the helperPod.yaml key ignores the template entirely.
run "a_provisioner_that_does_not_read_a_helper_pod_template_is_refused" {
  command = plan

  variables {
    install_local_path_helper_pod_resilience = true
  }

  override_data {
    target = data.kubernetes_config_map.local_path[0]
    values = {
      data = {
        "config.json" = "{\"nodePathMap\":[{\"node\":\"DEFAULT_PATH_FOR_NON_LISTED_NODES\",\"paths\":[\"/var/lib/rancher/k3s/storage\"]}]}"
      }
    }
  }

  expect_failures = [kubernetes_config_map_v1_data.local_path_helper_pod]
}
