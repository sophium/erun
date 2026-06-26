terraform {
  required_version = ">= 1.3"

  # Pure module: no `provider` blocks here. The caller (the root config the
  # erun-enable-hosting-edge skill generates, or any root that uses this module)
  # configures the helm + kubernetes providers — in-cluster service account in a
  # pod, KUBECONFIG on a laptop. This keeps the module usable via
  # `module { source = "git::…/terraform-erun-cluster-edge?ref=v<version>" }`,
  # referenced from erun's GitHub the same way `deploy` references the published
  # Helm chart from OCI.
  required_providers {
    helm = {
      source  = "hashicorp/helm"
      version = "~> 2.17"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 2.30"
    }
  }
}
