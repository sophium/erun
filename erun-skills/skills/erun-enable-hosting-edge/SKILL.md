---
name: erun-enable-hosting-edge
description: Stand up the public hosting edge for an erun cluster — a Traefik ingress controller, cert-manager, and a Cloudflare DNS-01 ClusterIssuer that issues wildcard TLS for the services zone — by applying the terraform-erun-cluster-edge module, and maintain, repair, and upgrade that edge afterwards by re-pinning the module ?ref to the env's erun version and re-applying to reconcile drift. Use when the user says "enable the hosting edge", "enable public hosting", "set up TLS ingress for erun", "apply the cluster edge", "set up cert-manager and traefik", "issue wildcard TLS for the services zone", "upgrade the hosting edge", "repair the cluster edge", "reconcile cert-manager and traefik", "bump the cluster edge to <version>", "maintain the public hosting edge", or any similar request to make a cluster's services reachable at public HTTPS hostnames.
---

# Enable the public hosting edge

This applies the `terraform-erun-cluster-edge` Terraform module to the cluster
so per-env services (`mcp.<tenant>-<env>.services.<base-domain>`) and the console
can be served over public HTTPS. The module installs Traefik + cert-manager and a
**Cloudflare DNS-01 `ClusterIssuer`** that issues a wildcard cert for the services
zone. It's idempotent — re-running reconciles.

## Prerequisites — check before applying

1. **A Cloudflare alias is attached to this environment.** That injects the
   delegated token into the pod as `CLOUDFLARE_API_TOKEN` (+ `CLOUDFLARE_ACCOUNT_ID`).
   ```sh
   [ -n "${CLOUDFLARE_API_TOKEN:-}" ] || { echo "No CLOUDFLARE_API_TOKEN — attach a Cloudflare alias: erun cloud init cloudflare, then erun cloud set <tenant>/<env> --alias <name>@cloudflare, then redeploy."; exit 1; }
   ```
2. **`terraform` and `kubectl` are available**, and `kubectl` targets the cluster
   you mean to expose (in a deployed env this is the in-cluster service account):
   ```sh
   command -v terraform >/dev/null && command -v kubectl >/dev/null || { echo "terraform + kubectl required"; exit 1; }
   kubectl cluster-info >/dev/null || { echo "kubectl is not pointed at a reachable cluster"; exit 1; }
   ```
3. **The services zone and ACME email** — from the platform config
   (`platform.serviceszone` / `platform.acmeemail`), e.g. `services.example.com`
   and `ops@example.com`. Ask the operator if they aren't already known.

## Apply

The module is **referenced from erun's GitHub** by Terraform's native module
addressing — not baked into the image, not cloned — pinned to the erun version
this env runs so the edge matches the deployed platform (the same way `deploy`
references the published Helm chart from OCI). Write a tiny root that declares
the providers and calls the module; `terraform init` fetches it. The Cloudflare
token rides in `TF_VAR_cloudflare_api_token`, never on the command line.

```sh
# Pin to the running erun version; off-pod, fall back to main.
version=$(erun version --no-registry 2>/dev/null | head -n1 | awk '{print $2}')
ref="v${version:-main}"; [ "$ref" = "vmain" ] && ref="main"

workdir=$(mktemp -d); cd "$workdir"
cat > main.tf <<EOF
terraform {
  required_providers {
    helm       = { source = "hashicorp/helm", version = "~> 2.17" }
    kubernetes = { source = "hashicorp/kubernetes", version = "~> 2.30" }
  }
}
# In a pod these use the in-cluster service account; on a laptop, KUBECONFIG.
provider "kubernetes" {}
provider "helm" {
  kubernetes {}
}

variable "cloudflare_api_token" {
  type      = string
  sensitive = true
}
variable "services_zone" { type = string }
variable "acme_email"    { type = string }

module "edge" {
  source = "git::https://github.com/sophium/erun.git//erun-devops/terraform-erun/modules/terraform-erun-cluster-edge?ref=${ref}"

  cloudflare_api_token = var.cloudflare_api_token
  services_zone        = var.services_zone
  acme_email           = var.acme_email
}
EOF

export TF_VAR_cloudflare_api_token="$CLOUDFLARE_API_TOKEN"
terraform init -input=false
terraform apply -input=false -auto-approve \
  -var "services_zone=<services-zone>" \
  -var "acme_email=<acme-email>"
```

While validating against a fresh zone, add
`-var acme_server=https://acme-staging-v02.api.letsencrypt.org/directory` to avoid
Let's Encrypt production rate limits, then re-apply without it for real certs. On a
cluster that already runs Traefik or cert-manager, add
`-var install_ingress_controller=false` and/or `-var install_cert_manager=false`.

## Verify

```sh
# Ingress controller + cert-manager are up.
kubectl get pods -n traefik -n cert-manager 2>/dev/null
# The issuer reaches Ready once it registers its ACME account (needs a real token + zone).
kubectl get clusterissuer erun-cloudflare
kubectl wait --for=condition=Ready clusterissuer/erun-cloudflare --timeout=120s
# The wildcard cert is issued (DNS-01 solves in the Cloudflare zone; may take a few minutes).
kubectl get certificate -n cert-manager
kubectl wait --for=condition=Ready certificate/erun-cloudflare-wildcard -n cert-manager --timeout=600s
```

A `Ready` ClusterIssuer + a `Ready` wildcard Certificate means the edge can
terminate TLS for `*.<services-zone>`. Route an env's service through it with
`erun expose` (which writes the PowerDNS record and the Host-routing Ingress);
reference the issuer on the Ingress as `cert-manager.io/cluster-issuer: erun-cloudflare`.

## Maintenance, repair & upgrade

This is an idempotent **apply** skill, not a scaffolder: it writes no durable local
files to drift — the only artifact is cluster state, and every run reconciles it. So
re-running *is* the maintenance path, not an error.

- **Detect.** If the edge is already applied (`kubectl get clusterissuer erun-cloudflare`
  succeeds), do not stop — enter maintenance mode and reconcile in place.
- **Upgrade.** Re-pin the module to the env's erun version, then re-apply. Recompute
  `ref` from the running `erun version` (the pin moves with `erun upgrade`, or an explicit
  target), set the module `source = "…terraform-erun-cluster-edge?ref=v<version>"` to that
  one version, then re-run the **Apply** flow above: `terraform init` fetches the new ref
  and `terraform apply` rolls it out. One erun version across the pin; bump it after
  `erun upgrade`.
- **Repair.** The apply already reconciles cert-manager, Traefik, and the Cloudflare
  DNS-01 `ClusterIssuer`, so healing drift is the same re-apply — no separate repair path,
  no new scaffold artifacts. Preview with `terraform plan` first and apply only the version
  pin + reconciliation; never let a re-apply clobber operator-owned cluster content.

## If issuance stalls

`kubectl describe certificate erun-cloudflare-wildcard -n cert-manager` and
`kubectl describe clusterissuer erun-cloudflare` show the ACME order/challenge
state. The usual causes: the Cloudflare token lacks `Zone:Read` + `DNS:Edit` on
the zone, or the services zone isn't actually delegated to Cloudflare yet
(`dig NS <services-zone>` should return Cloudflare name servers).
