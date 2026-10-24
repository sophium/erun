terraform {
  required_version = ">= 1.3"

  required_providers {
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "~> 5.0"
    }
  }
}

# Authenticates only via the CLOUDFLARE_API_TOKEN that erun injects into the pod;
# never accept the token as a variable.
provider "cloudflare" {}
