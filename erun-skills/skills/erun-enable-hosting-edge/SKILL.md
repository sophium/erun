---
name: erun-enable-hosting-edge
description: Stand up the public hosting edge for an erun cluster — a Traefik ingress controller, cert-manager, and a namespaced DNS-01 Issuer that issues wildcard TLS for the services zone — by applying the terraform-erun-cluster-edge module, and maintain, repair, and upgrade that edge afterwards by re-pinning the module ?ref to the env's erun version and re-applying to reconcile drift. Use when the user says "enable the hosting edge", "enable public hosting", "set up TLS ingress for erun", "apply the cluster edge", "set up cert-manager and traefik", "issue wildcard TLS for the services zone", "upgrade the hosting edge", "repair the cluster edge", "reconcile cert-manager and traefik", "bump the cluster edge to <version>", "maintain the public hosting edge", or any similar request to make a cluster's services reachable at public HTTPS hostnames.
---

# Enable the public hosting edge

This applies the `terraform-erun-cluster-edge` Terraform module to the cluster
so per-env services (`mcp.<tenant>-<env>.services.<base-domain>`) and the console
can be served over public HTTPS. The module installs Traefik + cert-manager and a
**namespaced DNS-01 `Issuer`** (in the env namespace) that issues a wildcard cert
for the services zone. The edge's Terraform state lives in the cluster, so
re-running reconciles the edge that is there instead of creating it again.

## Prerequisites — check before applying

1. **A Cloudflare alias is attached to this environment.** That injects the
   delegated token into the pod as `CLOUDFLARE_API_TOKEN` (+ `CLOUDFLARE_ACCOUNT_ID`).
   ```sh
   [ -n "${CLOUDFLARE_API_TOKEN:-}" ] || { echo "No CLOUDFLARE_API_TOKEN — attach a Cloudflare alias: erun cloud init cloudflare, then erun cloud set <tenant>/<env> --alias <name>@cloudflare, then redeploy."; exit 1; }
   ```
2. **`terraform`, `kubectl`, `helm` and `jq` are available** — the flow reads its
   plan with `jq` and finds an existing edge with `helm` — and `kubectl` targets
   the cluster you mean to expose (in a deployed env this is the in-cluster
   service account):
   ```sh
   for tool in terraform kubectl helm jq; do
     command -v "$tool" >/dev/null || { echo "$tool is required"; exit 1; }
   done
   kubectl cluster-info >/dev/null || { echo "kubectl is not pointed at a reachable cluster"; exit 1; }
   ```
   The module's Traefik chart needs **Kubernetes 1.30 or later**; on an older
   cluster the apply is refused (`chart requires kubeVersion: >=1.30.0-0`), so
   check before applying and upgrade the cluster (k3s) first if it is older:
   ```sh
   minor=$(kubectl get --raw /version | sed -n 's/.*"minor": *"\([0-9]*\).*/\1/p')
   [ "${minor:-0}" -ge 30 ] || { echo "Kubernetes 1.${minor} is older than the 1.30 the edge's Traefik chart needs; upgrade the cluster first."; exit 1; }
   ```
   In a deployed env the apply runs as the pod's ServiceAccount, which needs
   cluster-scoped rights (create namespaces + CRDs) and writes the edge's state
   to `kube-system` (a Secret, locked by a Lease). Confirm this env is a
   **platform account** — its SA bound to `cluster-admin`:
   ```sh
   kubectl auth can-i create namespaces >/dev/null && kubectl auth can-i create customresourcedefinitions >/dev/null \
     && kubectl auth can-i create secrets -n kube-system >/dev/null && kubectl auth can-i create leases.coordination.k8s.io -n kube-system >/dev/null \
     || { echo "SA lacks cluster-scoped rights. Make this env a platform account: erun init --platform-account (or set platformaccount: true), then redeploy from an admin-capable context so the chart binds the SA to cluster-admin."; exit 1; }
   ```
3. **The services zone and ACME email** — from the platform config
   (`platform.serviceszone` / `platform.acmeemail`), e.g. `services.example.com`
   and `ops@example.com`. Ask the operator if they aren't already known.

## Apply

The module is **referenced from erun's GitHub** by Terraform's native module
addressing — not baked into the image, not cloned — pinned to the erun version
this env runs so the edge matches the deployed platform (the same way `deploy`
references the published Helm chart from OCI). `terraform init -from-module`
fetches it into a scratch directory as the root, so every module input on this
page is a root variable set with `-var`; one file beside it configures the
providers and where the state lives. The Cloudflare token rides in
`TF_VAR_cloudflare_api_token`, never on the command line.

**The state lives in the cluster, not in the directory.** The edge is one per
cluster, so its Terraform state is the Secret `tfstate-default-erun-cluster-edge`
in `kube-system`, locked by the Lease `lock-tfstate-default-erun-cluster-edge`
beside it: a run started while another holds the lock is refused with the
holder's details instead of racing it. A later run from a fresh directory plans
against that state, which is what lets a re-run reconcile the edge rather than
create it again. The state carries what the module writes into Secrets (the
Cloudflare token, the TSIG key), so it is as sensitive as `kube-system`'s own.

**The blocks run in one shell session.** The setup block below changes into the
scratch root, exports what Terraform reads, and sets the edge's inputs as the
shell's positional parameters (`set -- -var ...`); a mode block further down
replaces those inputs; the plan-and-apply block uses all of it. Run setup, then
the mode block if any, then plan-and-apply in the same shell — as one command, or
saved together as one file and run with `sh` — never as separate commands: a
fresh shell has none of that state, and the plan-and-apply block stops on its
first line rather than plan without it.

```sh
# Pin to the running erun version; off-pod, fall back to main.
version=$(erun version --no-registry 2>/dev/null | head -n1 | awk '{print $2}')
ref="v${version:-main}"; [ "$ref" = "vmain" ] && ref="main"

workdir=$(mktemp -d); cd "$workdir"
terraform init -input=false -backend=false \
  -from-module="git::https://github.com/sophium/erun.git//erun-devops/terraform-erun/modules/terraform-erun-cluster-edge?ref=${ref}"
cat > erun-edge.tf <<'EOF'
terraform {
  backend "kubernetes" {
    secret_suffix = "erun-cluster-edge"
    namespace     = "kube-system"
  }
}
provider "kubernetes" {}
provider "helm" {
  kubernetes {}
}
EOF

# Terraform's providers and its kubernetes backend read neither KUBECONFIG nor
# ~/.kube/config. Hand them the kubeconfig kubectl uses (in a pod, the one its
# boot writes for the service account), so the edge, its state and every check
# here address one cluster.
unset KUBE_CONFIG_PATH KUBE_IN_CLUSTER_CONFIG
if [ -n "${KUBECONFIG:-}" ]; then export KUBE_CONFIG_PATHS="$KUBECONFIG"
elif [ -f "$HOME/.kube/config" ]; then export KUBE_CONFIG_PATHS="$HOME/.kube/config"
fi

export TF_VAR_cloudflare_api_token="$CLOUDFLARE_API_TOKEN"
terraform init -input=false

# The edge's inputs. Pass the same set on every run: the state remembers the
# edge, and a run that leaves a flag out plans to change it back.
set -- -var "services_zone=<services-zone>" -var "acme_email=<acme-email>"
```

Then plan and apply. On a cluster with no edge yet that is all it does. The edge
already exists when its kept state holds the issuer release or — for an edge
applied before its state was kept — when that release is deployed under the
names this run gives it or the module's defaults (`erun-cloudflare-issuer` in
`cert-manager`). On an existing edge these steps run between the plan and the
apply:

- **Never adopt an edge a tenant tree owns.** An edge applied with `erun terraform
  apply` from a `terraform-<tenant>/<env>` tree is owned by that tree's local
  state, `~/.erun/terraform/<tenant>/<env>/terraform.tfstate` on that environment's
  home volume, and two states must never own one edge. Before adopting, the flow
  reads every such state on this home volume and stops if one holds the issuer
  release (`Not adopting: <state> already holds <release>. Maintain this edge with
  erun terraform apply from that tenant tree.`). Another environment's home volume
  cannot be seen from here, so adoption also waits for the operator: the flow
  lists what it would import (`Adopting this edge imports:`) and stops (`Not
  adopting yet: ...`). Set `EDGE_ADOPT=confirmed` and re-run only once the operator
  confirms this skill applied the edge and no tenant tree did.
- **Adopt once, and only what this skill could have created.** Until this skill
  kept state every run started from none, so an edge it applied then has no state
  to match: planned from nothing it creates everything again and fails
  (`namespaces "cert-manager" already exists`, `cannot re-use a name that is
  still in use`). Adoption runs only while the kept state lacks the issuer
  release. It imports only what that stateless skill could have created — its
  root passed the module nothing but `cloudflare_api_token`, `services_zone` and
  `acme_email` — so only these objects, under these names, each one the plan
  would create and the cluster holds: the `cert-manager` namespace, the
  `cert-manager` and `traefik` releases, the `erun-cloudflare-cloudflare-token`
  Secret, and the `erun-cloudflare-issuer` release. The issuer release is imported
  last, so an adoption that was interrupted resumes on the next run and a finished
  one never runs again; then the plan is made again. Nothing else is ever
  imported: an object under other names, or one only a non-default input declares
  (`coredns-custom`, say), is left to the gate, so an operator's own object of the
  same name is never adopted.
- **Reconcile in place only.** No run's plan may destroy anything, and on an
  existing edge it may add nothing. A create there is an object adoption did not
  import, or a feature being turned on; a delete or a replacement — possible only
  once the state holds the edge, so checked on every run — is a flag the edge was
  applied with missing from the inputs, or a change of what the edge is. Either
  stops the run: show the operator the plan (`terraform show edge.tfplan`) and
  apply it only on their say-so. The two server-side-apply patches
  (`kubernetes_config_map_v1_data`, `kubernetes_annotations`) are the exception
  for a create: they cannot be imported, and creating one patches the object that
  is already there.
- **Upgrade Traefik's CRDs first.** Helm installs a chart's CRDs only where they
  are absent and never upgrades them, so a plan that moves the Traefik chart (an
  edge first applied on chart 33.x moving to 42.x, say) would leave the cluster on
  the old CRDs. The module's own `Middleware` and `IngressRoute` objects need no
  newer field, so the apply would still succeed; Traefik's own upgrade procedure
  still applies the new chart's CRDs before the upgrade, so this does the same,
  for the chart version the plan resolved (`-var traefik_chart_version=...`
  included). Without `helm_release.traefik` in the plan — `install_ingress_controller=false`
  — it is skipped: the CRDs belong to the controller already there.

**Every check fails closed.** Each `terraform`, `jq`, `helm` and `kubectl` call
the flow's decisions rest on stops the run when it fails, so a broken tool, an
unreadable state or an unreachable cluster never reads as "no edge" and skips
the gate.

```sh
# Needs the setup block directory, exports and inputs: run it in that same shell.
[ -f erun-edge.tf ] && [ "$#" -gt 0 ] || { echo "Run this in the shell that ran the setup block, with the edge inputs set (set -- -var ...)." >&2; exit 1; }

terraform plan -input=false -out=edge.tfplan "$@" || exit 1
terraform show -json edge.tfplan > edge.json || exit 1

# The edge exists when its kept state holds the issuer release or, applied before
# its state was kept, when that release is deployed under the names of this run or the defaults.
held=$(terraform state list) || exit 1
edge= deployed=
if printf '%s\n' "$held" | grep -qx 'helm_release.issuer'; then
  edge=kept
else
  release=$(jq -r '.resource_changes[] | select(.address == "helm_release.issuer") | .change.after | "\(.namespace)/\(.name)"' edge.json) || exit 1
  for id in "$release" cert-manager/erun-cloudflare-issuer; do
    found=$(helm list -q -n "${id%%/*}" --filter "^${id#*/}\$") || exit 1
    [ -n "$deployed" ] || [ -z "$found" ] || deployed=$id
  done
  edge=$deployed
fi

if [ -n "$deployed" ]; then
  # A tenant tree state (erun terraform apply) that holds this release owns the edge.
  for st in "$HOME"/.erun/terraform/*/*/terraform.tfstate; do
    [ -e "$st" ] || continue
    owned=$(jq --arg id "$deployed" 'any(.resources[]? | select(.mode == "managed" and .type == "helm_release")
      | .instances[]?.attributes; "\(.namespace)/\(.name)" == $id)' "$st") || exit 1
    [ "$owned" = false ] || { echo "Not adopting: $st already holds $deployed. Maintain this edge with erun terraform apply from that tenant tree." >&2; exit 1; }
  done

  # Adopt once, and only what the stateless skill could have created (its root passed
  # cloudflare_api_token, services_zone and acme_email alone), the issuer release last.
  jq -r '{"kubernetes_namespace.cert_manager": "cert-manager",
      "helm_release.cert_manager[0]": "cert-manager/cert-manager",
      "helm_release.traefik[0]": "traefik/traefik",
      "kubernetes_secret.cloudflare_api_token[0]": "cert-manager/erun-cloudflare-cloudflare-token",
      "helm_release.issuer": "cert-manager/erun-cloudflare-issuer"} as $stateless
    | [.resource_changes[] | select(.change.actions == ["create"]) | .change.after as $a
      | (if .type == "kubernetes_namespace" then $a.metadata[0].name
         elif .type == "kubernetes_secret" then "\($a.metadata[0].namespace)/\($a.metadata[0].name)"
         else "\($a.namespace)/\($a.name)" end) as $id
      | select($stateless[.address] == $id) | [.address, .type, $id]]
    | sort_by(.[0] == "helm_release.issuer") | .[] | @tsv' edge.json > adopt.tsv || exit 1
  : > import.tsv
  while IFS="$(printf '\t')" read -r address type id; do
    case $type in
      helm_release)         found=$(helm list -q -n "${id%%/*}" --filter "^${id#*/}\$") || exit 1 ;;
      kubernetes_namespace) found=$(kubectl get namespace "$id" --ignore-not-found -o name) || exit 1 ;;
      kubernetes_secret)    found=$(kubectl get secret "${id#*/}" -n "${id%%/*}" --ignore-not-found -o name) || exit 1 ;;
    esac
    [ -z "$found" ] || printf '%s\t%s\n' "$address" "$id" >> import.tsv
  done < adopt.tsv
  if [ -s import.tsv ]; then
    echo "Adopting this edge imports:" >&2; cat import.tsv >&2
    [ "${EDGE_ADOPT:-}" = confirmed ] || { echo "Not adopting yet: once the operator confirms this skill applied the edge and no tenant tree did, re-run with EDGE_ADOPT=confirmed." >&2; exit 1; }
    while IFS="$(printf '\t')" read -r address id; do
      terraform import -input=false "$@" "$address" "$id" </dev/null || exit 1
    done < import.tsv
    terraform plan -input=false -out=edge.tfplan "$@" || exit 1
    terraform show -json edge.tfplan > edge.json || exit 1
  fi
fi

# Reconcile in place only: no run destroys, and on an existing edge none creates
# but the two server-side-apply patches.
unsafe=$(jq -r --arg edge "$edge" '.resource_changes[] | select((.change.actions | index("delete")) or ($edge != ""
    and (.change.actions | index("create")) and (.type | IN("kubernetes_config_map_v1_data", "kubernetes_annotations") | not)))
  | .address' edge.json) || exit 1
[ -z "$unsafe" ] || { echo "Not applying: on an existing edge this plan creates or destroys: $unsafe" >&2; exit 1; }

# Traefik CRDs for the chart this plan installs, on an existing edge.
chart=$(jq -r '.resource_changes[] | select(.address == "helm_release.traefik[0]") | .change.after.version // empty' edge.json) || exit 1
if [ -n "$edge" ] && [ -n "$chart" ]; then
  helm show crds traefik --repo https://traefik.github.io/charts --version "$chart" > traefik-crds.yaml || exit 1
  kubectl apply --server-side --force-conflicts -f traefik-crds.yaml || exit 1
fi
terraform apply -input=false edge.tfplan
```

Each switch below is one more `-var` in that input set. While validating against
a fresh zone, add
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

**Storage reclamation on a pressured node.** `local-path` is the only storage
class these clusters ship, and the provisioner does both provisioning and
reclamation through a short-lived helper pod that it pins onto the node holding
the volume — the node under DiskPressure. kubelet's eviction manager admits and
evicts by whether a pod is *critical*, and a helper pod with no
`priorityClassName` resolves to priority 0, so it is turned away while the node
has conditions and evicted first when it has any. The provisioner then waits out
its 120-second create timeout and retries, and neither a new PVC nor the
deletion of an existing one can complete: on a node at 93% used, deleting a
50 GiB PVC frees no bytes. Adding `-var
install_local_path_helper_pod_resilience=true` merges
`priorityClassName: system-node-critical` into the distribution's own helper pod
template — keeping its image and every other field — and states the
disk-pressure toleration explicitly, because a helper pod template that declares
any tolerations at all stops the provisioner from adding its own default. It
also annotates the provisioner Deployment's pod template with a digest of the
template it wrote, since the provisioner reads that template once at startup and
on k3s has no reload (its 30-second refresh is a no-op without
`CONFIG_MOUNT_PATH`, which k3s's manifest does not set): a ConfigMap write alone
applies cleanly and changes nothing until the provisioner happens to restart.
Both the ConfigMap key and the annotation belong to the distribution's own
manifest, so a k3s upgrade that rewrites it reverts either — re-apply this
module if reclamation on a pressured node stops working after one. Off by
default, so an existing cluster's provisioner is untouched on a module upgrade.

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
set -- -var "services_zone=<services-zone>" -var "acme_email=<acme-email>" \
  -var dns01_provider=powerdns-rfc2136 \
  -var "powerdns_nameserver=$NS.$TNS.svc.cluster.local:53" \
  -var "rfc2136_tsig_key_name=$KEYNAME" \
  -var per_env_certificate_enabled=true -var "env_label=<tenant>-<env>"
```

Then run the plan-and-apply block with that input set, in the same shell, exactly
as **Apply** does.
`cloudflare_api_token` is not needed in this mode. On an edge already running the
Cloudflare solver this switch creates one Secret and destroys another, so the
plan stops for the operator's say-so, as a change of what the edge is.

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
set -- -var "services_zone=<services-zone>" -var "acme_email=<acme-email>" \
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
land it as the Secret the Issuer's webhook solver reads, then run the
plan-and-apply block, in the same shell, with this input set:

```sh
# Mint the per-env DNS-01 token (caller must hold a token for <tenant>/<env>).
TOKEN=$(curl -fsS -X POST -H "Authorization: Bearer $ERUN_API_TOKEN" \
  "$ERUN_API_URL/v1/environments/$ENV_ID/dns01-token" | jq -r .token)
kubectl -n "<tenant>-<env>" create secret generic "<tenant>-<env>-dns01-token" \
  --from-literal=token="$TOKEN" --dry-run=client -o yaml | kubectl apply -f -

set -- -var "services_zone=<services-zone>" -var "acme_email=<acme-email>" \
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
# http answers a permanent redirect to the same host on https's own port, and
# https carries HSTS.
curl -sSI "http://<base-domain>" | grep -i -E '^(HTTP|location)'   # 301, Location: https://<base-domain>/
curl -sSI "https://<base-domain>" | grep -i strict-transport-security
```

A `Location` that names a port (`https://<base-domain>:8443/`) is the redirect
pointing at Traefik's in-pod listen port instead of the published 443: every
plaintext visitor is sent somewhere nothing answers.

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

This is an **apply** skill, not a scaffolder: it writes no repository files to
drift. Besides the edge itself its one durable artifact is the edge's state, kept
in the cluster (see **Apply**), and every run plans against it. So re-running the
**Apply** flow *is* the maintenance path, not an error.

- **Detect.** The edge exists when its kept state holds the issuer release or,
  for one applied before its state was kept, when that release is deployed
  (`helm list -n cert-manager --filter '^erun-cloudflare-issuer$'` for the
  module's default names); the **Apply** flow checks exactly that and then adopts
  and reconciles in place, so do not stop. An edge applied with `erun terraform
  apply` from a tenant's `terraform-<tenant>/<env>` tree keeps its state there,
  at `~/.erun/terraform/<tenant>/<env>/terraform.tfstate` on that environment's
  home volume: maintain it with `erun terraform apply`, never with this skill.
  The **Apply** flow refuses to adopt an edge such a state on this home volume
  holds, and adopts any other only on the operator's confirmation, so two states
  never own one release.
- **Adopt once.** An edge this skill applied before it kept state has none. The
  first re-run the operator confirms imports the objects that skill created, the
  issuer release last, and from then on plans against the kept state and never
  imports again; if that run is interrupted, the next one imports what is still
  missing.
- **Upgrade.** Re-pin the module to the env's erun version, then re-run the
  **Apply** flow: recompute `ref` from the running `erun version` (the pin moves with
  `erun upgrade`, or an explicit target) so `terraform init -from-module` fetches
  that one version, and the plan rolls it out, after Traefik's CRDs for the chart
  it resolves. One erun version across the pin; bump it after `erun upgrade`.
- **Repair.** Drift in what the edge's objects carry is the same re-run: the plan
  updates them in place — no separate repair path, no new scaffold artifacts. Pass
  the flags the edge was applied with; never let a re-run clobber operator-owned
  cluster content, which is why a plan that creates or destroys on an existing edge
  stops for the operator.
- **Locked state.** A run refused with `the state is already locked by another
  terraform client` names the holder; wait for it to finish. Release the Lease
  (`terraform force-unlock <ID>`, from a root written the same way) only once that
  run is confirmed gone.
- **Clean up.** Remove the scratch directory when the run ends (`rm -rf
  "$workdir"`): its saved plan carries the Cloudflare token. Re-running reconciles
  rather than accumulates, so nothing else is left behind. Tearing the edge down
  (removing cert-manager, Traefik, the `Issuer`) is `terraform destroy` with the
  edge's inputs, from a root written the same way so it reads the kept state — a
  deliberate, high-blast-radius operator action that drops TLS for the whole
  services zone; point the operator at it, never run it as a side effect of
  maintenance.

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
