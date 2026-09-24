# Locks the configuration a cluster under DiskPressure needs in order to heal
# its own storage. The local-path provisioner does BOTH provisioning and
# reclamation through a short-lived helper pod, and it pins that pod to the node
# holding the volume by setting spec.nodeName itself -- so the scheduler never
# sees it and the only admission that applies is kubelet's. kubelet's eviction
# manager rejects every non-critical pod while the node's conditions are
# non-empty, and refuses to evict a critical one, where critical is
# `IsCriticalPod`: a static or mirror pod, or one whose resolved spec.priority is
# at least scheduling.SystemCriticalPriority. The helper pod ships with no
# priorityClassName, so it is an ordinary zero-priority pod that is rejected
# under DiskPressure and evictable the moment it runs -- the node cannot free the
# space causing the pressure it would have to relieve to run the thing that frees
# the space.
#
# A toleration is NOT what is missing, and this suite pins that too: the
# provisioner appends the disk-pressure toleration itself (`lpvTolerations`,
# v0.0.32+; a blanket `Exists` toleration before that), so the pod has always
# tolerated the taint and adding one fixes nothing. The priority class is the
# fix, and the toleration is stated explicitly beside it only because declaring
# `tolerations` at all suppresses the provisioner's own default.
#
# Runs entirely against mocked providers: no real cluster is touched, so this
# pins the configuration the provisioner reads rather than a live scheduling
# outcome. The live half of the evidence -- a scratch PVC provisioning and
# reclaiming through a real helper pod on a real node -- is not something a
# provider mock can express, and is reported with the change instead.

mock_provider "helm" {}

mock_provider "kubernetes" {
  # The provisioner's ConfigMap as k3s ships it
  # (manifests/local-storage.yaml). The helper pod template carries its image and
  # no scheduling fields at all, which is the state the defect is about.
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

# Adding this module version to an already-applied cluster must leave its
# provisioner completely untouched until an operator opts in.
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

# The reported failure, stated as the property that was missing. On the unfixed
# module the helper pod template carries no priorityClassName, so the pod
# kubelet admits and ranks is an ordinary zero-priority one: rejected outright
# while the node's conditions are non-empty, and first in line to be evicted
# when they are, which is how the reported `0/1 Evicted` helper pod arises. Both
# the provision path and the delete path run this same pod, so neither a new PVC
# nor the deletion of an existing one can complete on the node that needs it.
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

  # The reproduction of the reported state: k3s's own template, no
  # priorityClassName, is what the pod kubelet rejected under DiskPressure was
  # built from. It must not be what this module writes.
  assert {
    condition     = try(yamldecode(kubernetes_config_map_v1_data.local_path_helper_pod[0].data["helperPod.yaml"]).spec.priorityClassName, "") == "system-node-critical"
    error_message = "the helper pod must carry system-node-critical. Without a priorityClassName its spec.priority resolves to 0, kubelet's eviction manager admits it only while the node has no conditions and evicts it first when the node has any, and this pod is pinned straight onto the pressured node by the provisioner -- so neither provisioning nor reclamation can complete there and a deleted PVC frees no bytes"
  }
}

# The neighbouring state, pinned so the fix is not mistaken for the toleration.
# The provisioner appends the disk-pressure toleration itself when the template
# declares none, but declaring any tolerations at all suppresses that default,
# so the merged template has to state the one it was getting implicitly --
# otherwise enabling this feature would trade a missing priority class for a
# missing toleration and leave the pod inadmissible by a different route.
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

# A cluster whose distribution already gave the helper pod a toleration of its
# own must keep it; this merge adds scheduling fields rather than owning them.
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

# The template is merged, never replaced: naming the helper image here would
# break a mirrored or air-gapped cluster whose own manifest chose a different
# one, a strictly worse failure than the one being fixed.
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

# Writing the key is not enough on its own, and this is the false success the
# annotation closes: the provisioner reads the template once at startup and
# holds it for the life of the process. It does run a 30s reload loop, but
# `refreshHelperPod` returns immediately unless CONFIG_MOUNT_PATH is set and
# k3s's Deployment declares only POD_NAMESPACE -- so on the distribution this
# platform's clusters run, a ConfigMap write alone applies cleanly, reports
# success, and changes nothing until the provisioner restarts for some other
# reason.
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

  # Derived from the template actually written, not a constant: asserted against
  # the ConfigMap resource's own value, which the module yamlencodes, so a
  # hardcoded digest that never changes -- and so never rolls the provisioner on
  # a real edit -- fails here rather than silently.
  assert {
    condition     = kubernetes_annotations.local_path_provisioner_rollout[0].template_annotations["erun.io/helper-pod-template-digest"] == sha256(kubernetes_config_map_v1_data.local_path_helper_pod[0].data["helperPod.yaml"])
    error_message = "the rollout annotation must be a digest of the helper pod template this apply writes, so that changing that template is what causes the restart"
  }
}

# A distribution that names its provisioner ConfigMap something else can still
# be configured, rather than silently writing a key nothing reads.
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

# The false success this refuses: a provisioner older than helperPod.yaml
# support ships no such key, ignores whatever is written here, and the apply
# would report success while the helper pod stays exactly as inadmissible as
# before. The key's presence is the only plan-time signal that this version
# reads a template.
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
