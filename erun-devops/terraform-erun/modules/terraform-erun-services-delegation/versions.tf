terraform {
  required_version = ">= 1.3"

  # Pure module: no `provider` blocks here. The caller configures the cloudflare
  # provider with a token that can edit the parent zone's DNS. This keeps the
  # module usable via
  # `module { source = "git::…/terraform-erun-services-delegation?ref=v<version>" }`,
  # referenced from erun's GitHub the same way `deploy` references the published
  # Helm chart from OCI.
  required_providers {
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "~> 5"
    }
  }
}
