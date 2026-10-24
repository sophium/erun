terraform {
  required_version = ">= 1.3"

  # Pure module: the caller configures the helm and kubernetes providers.
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
