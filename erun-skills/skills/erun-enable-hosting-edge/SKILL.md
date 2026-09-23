---
name: erun-enable-hosting-edge
description: Stand up the public hosting edge for an erun cluster — a Traefik ingress controller, cert-manager, and a namespaced DNS-01 Issuer that issues wildcard TLS for the services zone — by applying the terraform-erun-cluster-edge module, and maintain, repair, and upgrade that edge afterwards by re-pinning the module ?ref to the env's erun version and re-applying to reconcile drift. Use when the user says "enable the hosting edge", "enable public hosting", "set up TLS ingress for erun", "apply the cluster edge", "set up cert-manager and traefik", "issue wildcard TLS for the services zone", "upgrade the hosting edge", "repair the cluster edge", "reconcile cert-manager and traefik", "bump the cluster edge to <version>", "maintain the public hosting edge", or any similar request to make a cluster's services reachable at public HTTPS hostnames.
---

# Enable the public hosting edge

This applies the `terraform-erun-cluster-edge` Terraform module to the cluster
so per-env services (`mcp.<tenant>-<env>.services.<base-domain>`) and the console
can be served over public HTTPS. The module installs Traefik + cert-manager and a
**namespaced DNS-01 `Issuer`** (in the env namespace) that issues a wildcard cert
for the services zone. It's idempotent — re-running reconciles.

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
   In a deployed env the apply runs as the pod's ServiceAccount, which needs
   cluster-scoped rights (create namespaces + CRDs). Confirm this env is a
   **platform account** — its SA bound to `cluster-admin`:
   ```sh
   kubectl auth can-i create namespaces >/dev/null && kubectl auth can-i create customresourcedefinitions >/dev/null \
     || { echo "SA lacks cluster-scoped rights. Make this env a platform account: erun init --platform-account (or set platformaccount: true), then redeploy from an admin-capable context so the chart binds the SA to cluster-admin."; exit 1; }
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
cluster that already runs cert-manager, add `-var install_cert_manager=false`. On a
cluster that already runs an ingress controller add **both**
`-var install_ingress_controller=false -var manage_transport_policy=false` — the
second is what says the transport policy below belongs to the controller that is
already there.

**Plaintext and HSTS are the edge's job, not each Ingress's.** The module
redirects the plaintext entrypoint to the secure one (301) and serves
`Strict-Transport-Security` there, for every host the controller routes. That
placement is the point: Traefik answers `:80` for every rule it routes, so until
the entrypoint upgrades the scheme, a visitor who types the bare domain stays on
http — and so does any *relative* redirect issued behind the edge, because a
relative `Location` inherits whatever scheme the browser started on. A host
added later therefore cannot forget it, and no application can be left holding
the pieces. The policy is declared *on the controller this module installs*, so
`install_ingress_controller=false` leaves it nowhere to land — and the module
refuses that combination while `manage_transport_policy` is left at its default
`true`, rather than applying cleanly while `http_redirect_enabled` /
`hsts_enabled` and the HSTS settings do nothing and every public host serves
cleartext. Set `manage_transport_policy=false` and the module declares none of
it, but hands you what it would have carried at the **`edge_transport_policy`**
output: the redirect and HSTS entrypoint arguments, the HSTS `Middleware`
object, and — when the ACME exemption below is on — `redirect_objects`. Since
none of this module's resources are yours to inspect, that output also echoes
the resolved `http01_acme_challenges_present`, the one fact the module cannot
observe and has to be told; read it back there rather than assuming the
declaration reached the module. Apply
that to the controller that is already there instead of hand-rolling an overlay
that duplicates these defaults and then drifts from them — on k3s that is a
`HelmChartConfig`; the output is the policy rather than the mechanism, so it
fits whatever your controller takes.

**If any certificate here is solved over HTTP-01, the blanket redirect will
starve it.** This module's own Issuer is always DNS-01, but an Issuer *you*
bring need not be, and the entrypoint-wide redirect has no path predicate:
Traefik applies it to `/.well-known/acme-challenge/` too, Let's Encrypt follows
the 301 to https, and the solver's Ingress has no TLS block there — so the
certificate fails to renew, weeks later, as an expiry rather than as a plan
error. The module cannot see your Issuer, so say so: set
`-var http01_acme_challenges_present=true`, and the module refuses to produce
the blanket redirect until you set `-var acme_challenge_path_exempt=true`,
which carries the redirect as a `Middleware` plus a catch-all router whose rule
excludes the challenge prefix instead. That keeps the solver reachable and
still upgrades every other request — because that router declares an explicit
priority. Traefik otherwise derives a priority from a rule's length, and the
catch-all's own rule is shorter than the `Host(...) && PathPrefix` rules it has
to be applied before, so without the declared priority it loses to them and the
redirect silently does not run on those hosts. The negated rule is what
makes the priority safe: the challenge path does not match the router at all,
so no priority it carries can take a solver's request away from the router that
answers it. The exempted path is then served in
cleartext on every host the edge routes — browsers that have read the HSTS
header still refuse it — so turn it on because a real HTTP-01 Issuer needs it,
not speculatively. Moving those hosts to a DNS-01 Issuer removes the need
entirely.

`hsts_max_age_seconds` defaults to `86400` (one day), with
`hsts_include_subdomains` and `hsts_preload` off. HSTS cannot be recalled early:
a browser that has read the header refuses plaintext for that host until the
max-age expires, so the first value is deliberately short and the two switches
that bind names beyond the hosts this module serves stay off until every name
under the domain is verified https-only. Raise `hsts_max_age_seconds` towards
`31536000` first, then `hsts_include_subdomains`, then `hsts_preload`. The
docs host is published to Cloudflare Pages rather than routed through the edge,
so it carries its own `_headers` in `erun-docs/static/`, not this module's.

**In-cluster resolution of the platform's own names.** k3s's bundled CoreDNS ends
its default Corefile in `forward . /etc/resolv.conf`, so every name outside
`cluster.local` — including the platform's own published hostnames — resolves
through whatever DNS the node happens to use. If that resolver ever serves a stale
or wrong answer for one of those names, cert-manager's HTTP-01 self-check fails the
same way at issuance and at every unattended renewal, with the cause buried in a
Challenge's status. Add `-var install_coredns_forward=true -var
"base_domain_name=<base-domain>"` (the platform config's `basedomain`, e.g.
`example.com`) to declare a CoreDNS custom server block that forwards it to public
resolvers directly, independent of the node. This is opt-in and defaults to false
so an existing cluster's DNS behavior does not change on a module upgrade; add
`-var 'coredns_forward_upstreams=["<resolver>", ...]'` on an air-gapped or
policy-constrained cluster that must not reach public resolvers.

Three things are refused at plan time rather than allowed to fail later. A
malformed upstream (a typo'd address, a stray comma, a hostname where an IP was
meant) would write a syntactically invalid server block that CoreDNS only chokes
on at its next restart — a node drain or an upgrade, by which point the cluster
has no DNS and the cause is an apply from days earlier. A cluster-internal
`base_domain_name` such as `cluster.local` would shadow CoreDNS's own kubernetes
plugin and kill `.svc.cluster.local` resolution outright. And a Corefile that
does not `import /etc/coredns/custom/*.server` means the entry would be written
and never read, so the apply would report success while in-cluster resolution
stayed exactly as broken as before.

**Delegated services zone (PowerDNS DNS-01).** Once the services zone is delegated
off Cloudflare to the platform's own PowerDNS, the Cloudflare DNS-01 solver can no
longer prove control of it — switch the solver to RFC2136 (DNS UPDATE + TSIG) and
add a per-env wildcard cert. The TSIG key is minted by the `erun-powerdns` chart;
read it back and pass it in:

```sh
NS="<tenant>-powerdns"; TNS="<platform-namespace>"   # e.g. frs-powerdns / frs-prod
TSIG=$(kubectl -n "$TNS" get secret "$NS-tsig" -o jsonpath='{.data.tsig-secret}' | base64 -d)
KEYNAME=$(kubectl -n "$TNS" get secret "$NS-tsig" -o jsonpath='{.data.key-name}' | base64 -d)
export TF_VAR_rfc2136_tsig_secret="$TSIG"
terraform apply -input=false -auto-approve \
  -var "services_zone=<services-zone>" -var "acme_email=<acme-email>" \
  -var dns01_provider=powerdns-rfc2136 \
  -var "powerdns_nameserver=$NS.$TNS.svc.cluster.local:53" \
  -var "rfc2136_tsig_key_name=$KEYNAME" \
  -var per_env_certificate_enabled=true -var "env_label=<tenant>-<env>"
```

`cloudflare_api_token` is not needed in this mode.

**Brokered DNS-01 (multi-tenant clusters).** RFC2136 above hands the cluster one
zone-wide TSIG key — safe only on the single-tenant platform cluster. On a cluster
shared by multiple tenants that key is an impersonation hole (any namespace could
issue any tenant's cert). Per-tenant Issuers instead need a namespaced Issuer whose
challenges route through a per-cluster cert-manager webhook shim to the DNS-01
broker (`erun-backend-api`), which authorizes each challenge against the caller's
own subzone — the env presents a scoped token, not a DNS credential. `erun expose`
provisions those per-tenant Issuers itself (not this module), but they can only ever
reach `Ready` once the webhook shim is installed, so this module needs to install it
even when the *platform's own* wildcard stays on `cloudflare` or `powerdns-rfc2136`.

**`dns01_provider` and `install_dns01_webhook` are independent switches.**
`dns01_provider` picks the solver for the platform's *own* wildcard Issuer only.
`install_dns01_webhook` (unset by default) installs the per-cluster webhook shim;
left unset it defaults to `true` exactly when `dns01_provider = "powerdns-broker"`
(back-compat with the module's original single-switch behavior). Set it explicitly
to decouple the two — the shape a multi-tenant platform on RFC2136 needs:

```sh
terraform apply -input=false -auto-approve \
  -var "services_zone=<services-zone>" -var "acme_email=<acme-email>" \
  -var dns01_provider=powerdns-rfc2136 \
  -var "powerdns_nameserver=$NS.$TNS.svc.cluster.local:53" \
  -var "rfc2136_tsig_key_name=$KEYNAME" \
  -var install_dns01_webhook=true \
  -var "broker_url=https://api.<platform-tenant>-<platform-env>.services.<services-zone>/v1/dns01" \
  -var per_env_certificate_enabled=true -var "env_label=<tenant>-<env>"
```

`dns01_webhook_image` is left unset above on purpose: the module defaults it to
`ghcr.io/sophium/erun-dns01-webhook` at the version its own bundled
`chart-dns01-webhook/Chart.yaml` is released at, so the shim can never disagree
with the module — no pin required. Pass `-var "dns01_webhook_image=..."` only to
override that (e.g. to test a build ahead of a release).

This keeps the platform's own wildcard on the proven RFC2136 path (see the TSIG
setup above) while making the webhook shim available for every tenant's own
brokered Issuer. Setting `dns01_provider=powerdns-broker` *without*
`install_dns01_webhook=true` is refused at apply time (a precondition) — it would
render the platform's own Issuer against a solver group nothing serves, which is
what leaves a namespace undeletable when its Challenge can never be presented or
cleaned up.

To route the platform's own wildcard through the broker too, apply in full broker
mode instead. Per env (tenant), mint the env's DNS-01 token from the backend and
land it as the Secret the Issuer's webhook solver reads, then apply:

```sh
# Mint the per-env DNS-01 token (caller must hold a token for <tenant>/<env>).
TOKEN=$(curl -fsS -X POST -H "Authorization: Bearer $ERUN_API_TOKEN" \
  "$ERUN_API_URL/v1/environments/$ENV_ID/dns01-token" | jq -r .token)
kubectl -n "<tenant>-<env>" create secret generic "<tenant>-<env>-dns01-token" \
  --from-literal=token="$TOKEN" --dry-run=client -o yaml | kubectl apply -f -

terraform apply -input=false -auto-approve \
  -var "services_zone=<services-zone>" -var "acme_email=<acme-email>" \
  -var dns01_provider=powerdns-broker \
  -var "broker_url=https://api.<platform-tenant>-<platform-env>.services.<services-zone>/v1/dns01" \
  -var "dns01_token_secret_name=<tenant>-<env>-dns01-token" \
  -var per_env_certificate_enabled=true -var "env_label=<tenant>-<env>" \
  -var "env_namespace=<tenant>-<env>"
```

As above, `dns01_webhook_image` is left to the module's own default.

The webhook shim installs once per cluster; each tenant adds only its own Issuer +
token Secret. Neither `cloudflare_api_token` nor the TSIG key is needed in either
broker-adjacent mode above.

## Verify

```sh
# Ingress controller + cert-manager are up.
kubectl get pods -n traefik -n cert-manager 2>/dev/null
# The Issuer is namespaced — it lives in the env namespace (issuer_namespace),
# e.g. frs-prod; cert-manager for an apex-only edge with no env.
NS="<issuer-namespace>"
# The Issuer reaches Ready once it registers its ACME account (needs a real token + zone).
kubectl get issuer -n "$NS" erun-cloudflare
kubectl wait --for=condition=Ready issuer/erun-cloudflare -n "$NS" --timeout=120s
# The wildcard cert is issued (DNS-01 solves in the services zone; may take a few minutes).
kubectl get certificate -n "$NS"
kubectl wait --for=condition=Ready certificate/erun-cloudflare-wildcard -n "$NS" --timeout=600s
```

The transport policy is verified from **outside** the cluster, because that is
where it is observed: reading the Middleware object back proves it was created,
not that the entrypoint serves it or that a redirect answers before an
application does.

```sh
# http answers a permanent redirect, and https carries HSTS.
curl -sSI "http://<base-domain>" | head -1     # HTTP/1.1 301 Moved Permanently
curl -sSI "https://<base-domain>" | grep -i strict-transport-security
```

When `install_coredns_forward` is set, confirm the cluster can actually resolve the
base domain before trusting issuance to it — this is exactly the check that catches
a resolver problem before a Certificate sits pending with the cause buried in a
Challenge's status. Terraform has no reliable way to run this from inside the
module itself (it would need to schedule a Job/Pod as a side effect of `apply`,
adding image-pull and RBAC requirements to a declarative-only module for what is
fundamentally a smoke test), so it's a manual step here instead:

```sh
# Look up a name the platform actually publishes under the base domain, NOT the
# bare apex: an apex commonly has no A record at all, so `nslookup <base-domain>`
# returns NXDOMAIN on a perfectly healthy forward and reads as a failure.
kubectl run coredns-forward-check --rm -i --restart=Never --image=busybox:1.36 \
  -- nslookup "api.<base-domain>"
```

If nothing is published under the base domain yet, ask for a record type the zone
always has instead of an address that may not exist:

```sh
kubectl run coredns-forward-check --rm -i --restart=Never --image=busybox:1.36 \
  -- nslookup -type=soa "<base-domain>"
```

A timeout, or an answer that does not come from the configured upstreams, means
the forward zone isn't resolving yet — CoreDNS reloads `coredns-custom`
automatically, but allow ~80 seconds after the apply before treating a failure
here as real. An `NXDOMAIN` for a name that genuinely has no record is not a
failure of the forward; that is the trap this wording exists to avoid.

A `Ready` Issuer + a `Ready` wildcard Certificate means the edge can
terminate TLS. Route an env's service through it with `erun expose` — it writes the
PowerDNS record and applies a Host-routing Ingress that serves `https` by default,
referencing the **per-env wildcard cert Secret** (`<tenant>-<env>-wildcard-tls`)
directly. It sets no `cert-manager.io/issuer` annotation: the per-env cert
is pre-issued (`per_env_certificate_enabled`), so one cert covers every exposed
service and exposing another adds only an Ingress.

## Maintenance, repair & upgrade

This is an idempotent **apply** skill, not a scaffolder: it writes no durable local
files to drift — the only artifact is cluster state, and every run reconciles it. So
re-running *is* the maintenance path, not an error.

- **Detect.** If the edge is already applied (`kubectl get issuer -n <issuer-namespace> erun-cloudflare`
  succeeds — the env namespace, e.g. frs-prod), do not stop — enter maintenance mode and reconcile in place.
- **Upgrade.** Re-pin the module to the env's erun version, then re-apply. Recompute
  `ref` from the running `erun version` (the pin moves with `erun upgrade`, or an explicit
  target), set the module `source = "…terraform-erun-cluster-edge?ref=v<version>"` to that
  one version, then re-run the **Apply** flow above: `terraform init` fetches the new ref
  and `terraform apply` rolls it out. One erun version across the pin; bump it after
  `erun upgrade`.
- **Repair.** The apply already reconciles cert-manager, Traefik, and the namespaced
  DNS-01 `Issuer`, so healing drift is the same re-apply — no separate repair path,
  no new scaffold artifacts. Preview with `terraform plan` first and apply only the version
  pin + reconciliation; never let a re-apply clobber operator-owned cluster content.
- **Clean up.** Nothing local to prune (each run uses a throwaway `mktemp -d`), and
  re-apply reconciles rather than accumulates — so cleanup isn't part of the normal
  path. Tearing the edge down (removing cert-manager, Traefik, the `Issuer`)
  is `erun terraform destroy` — a deliberate, high-blast-radius operator action that
  drops TLS for the whole services zone; point the operator at it, never run it as a
  side effect of maintenance.

## If issuance stalls

`kubectl describe certificate erun-cloudflare-wildcard -n <issuer-namespace>` and
`kubectl describe issuer erun-cloudflare -n <issuer-namespace>` (the env namespace,
e.g. frs-prod) show the ACME order/challenge state. The usual causes: the Cloudflare
token lacks `Zone:Read` + `DNS:Edit` on the zone, or the services zone isn't actually
delegated to Cloudflare yet (`dig NS <services-zone>` should return Cloudflare name servers).

A self-check failure specifically (`describe challenge` in the issuer namespace shows
`failed to perform self check GET request ... dial tcp: lookup <host> ... no such
host`) while the same name resolves fine from outside the cluster points at the
node's resolver, not Cloudflare or the zone: confirm with
`kubectl run dns-check --rm -i --restart=Never --image=busybox:1.36 -- nslookup
<host>` from inside the cluster, and if that fails while a public resolver succeeds,
apply `install_coredns_forward=true` (see **Apply** above) rather than waiting for
the node's resolver to recover on its own.
