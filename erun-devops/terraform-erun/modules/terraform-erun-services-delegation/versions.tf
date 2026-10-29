terraform {
  required_version = ">= 1.3"

  # Pure module: the caller configures the cloudflare provider.
  required_providers {
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "~> 5"
    }
  }
}
