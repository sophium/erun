---
name: erun-enable-hosting-edge
description: Stand up the public hosting edge for an erun cluster — a Traefik ingress controller, cert-manager, and a Cloudflare DNS-01 ClusterIssuer that issues wildcard TLS for the services zone — by applying the terraform-erun-cluster-edge module. Use when the user says "enable the hosting edge", "enable public hosting", "set up TLS ingress for erun", "apply the cluster edge", "set up cert-manager and traefik", "issue wildcard TLS for the services zone", or any similar request to make a cluster's services reachable at public HTTPS hostnames.
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

Get the module (it lives in the erun repo) and apply it. The Cloudflare token is
passed through `TF_VAR_cloudflare_api_token` — never on the command line, so it
stays out of shell history and process args.

```sh
workdir=$(mktemp -d)
git clone --depth 1 https://github.com/sophium/erun "$workdir/erun"
cd "$workdir/erun/erun-devops/terraform-erun/modules/terraform-erun-cluster-edge"

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

## If issuance stalls

`kubectl describe certificate erun-cloudflare-wildcard -n cert-manager` and
`kubectl describe clusterissuer erun-cloudflare` show the ACME order/challenge
state. The usual causes: the Cloudflare token lacks `Zone:Read` + `DNS:Edit` on
the zone, or the services zone isn't actually delegated to Cloudflare yet
(`dig NS <services-zone>` should return Cloudflare name servers).
