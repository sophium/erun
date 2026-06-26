terraform {
  required_version = ">= 1.3"

  required_providers {
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "~> 5.0"
    }
  }
}

# Authentication is delegated entirely to the Cloudflare provider's native
# CLOUDFLARE_API_TOKEN environment variable, so this provider block is intentionally
# empty. erun injects an account-scoped, delegated API token (account-level Zone:Edit
# + DNS:Edit) into the runtime pod; do not hardcode the token here or accept it as a
# Terraform variable.
provider "cloudflare" {}
