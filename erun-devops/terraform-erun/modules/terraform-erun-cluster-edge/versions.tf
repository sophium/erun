terraform {
  required_version = ">= 1.3"

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

# Both providers target the cluster the module runs against. config_path is empty
# by default so they use the standard loading rules: the in-cluster service
# account when applied from inside a pod (the production path — erun injects the
# Cloudflare token there), or KUBECONFIG / ~/.kube/config on a laptop / CI.
provider "kubernetes" {
  config_path = var.kubeconfig_path != "" ? pathexpand(var.kubeconfig_path) : null
}

provider "helm" {
  kubernetes {
    config_path = var.kubeconfig_path != "" ? pathexpand(var.kubeconfig_path) : null
  }
}
