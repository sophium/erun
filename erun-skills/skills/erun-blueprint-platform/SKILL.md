---
name: erun-blueprint-platform
description: Blueprint the deploy artifacts for a hosted erun platform — a per-env Terraform tree (terraform-<tenant>/) whose modules wrap erun's published Terraform modules, and the per-env Helm values overlays plus thin umbrella charts that reference erun's published OCI charts — all version-pinned to the erun release the environment runs; also maintains, repairs, and upgrades an existing terraform-<tenant>/ tree and its <tenant>-<component> umbrellas in place, re-pinning every erun reference to the target version and filling gaps against this contract. Use when the user says "blueprint the platform", "scaffold the platform terraform", "set up the platform helm charts and terraform", "create the terraform-<tenant> structure", "blueprint erun platform deploy", "set up platform deploy artifacts", "upgrade the platform terraform", "repair the platform charts", "reconcile the terraform-<tenant> tree", "bump the platform to <version>", "maintain the platform deploy artifacts", or any similar request to lay down or update the Terraform + Helm wiring an operator applies to stand up a hosted erun platform.
---

# Blueprint a hosted erun platform's deploy artifacts

Use this skill in the **agent env** (the dev workbench, e.g. `<tenant>-local`)
to lay down the deploy artifacts an operator later applies in the **runtime
env** (e.g. `<tenant>-prod`) to stand up a hosted erun platform. It produces
two things, both of which **reference erun's published artifacts** rather than
copy them, pinned to the erun version the environment runs:

- a per-env **Terraform** tree under `terraform-<tenant>/` whose tenant modules
  wrap erun's published Terraform modules (sourced from GitHub by `ref`), and
- the per-env **Helm** values overlay (`values.<env>.yaml`) that `erun deploy`
  consumes, plus thin umbrella `Chart.yaml` wrappers that depend on erun's
  published **OCI** charts.

This skill is for the tenant that **deploys the erun platform itself**
(erunpaas) — its umbrellas wrap erun's *own* component charts (`erun-backend-*`,
`erun-powerdns`, `erun-zitadel`, `erun-docs`). A regular tenant that only runs agent envs never
uses it, and it never wraps the runtime `erun-devops` chart (see What this is
not).

**Deploy is sourceless; the Helm umbrellas are optional.** A runtime env deploys
the platform **by reference**: `erun deploy <tenant> <env> --components erun-backend-…`
installs each published `oci://<registry>/charts/erun-<component>` chart directly,
threading `tenant`/`environment` + the env's config as `--set`, with **no local
checkout** (a runtime env has no worktree). Produce the `<tenant>-<component>`
umbrella charts below **only** for the optional cases — real-time patching, or a
genuine per-env chart-value override the env config can't express. The Terraform
tree (the edge) is the part every hosted platform still needs; the umbrellas are not.

The reusable charts and modules stay erun's; the tenant owns only the thin
wrappers and the env-specific values. This skill packages ERun's accumulated
best practices for platform deploy wiring — the conventions encoded here are
the contract; do not freelance them.

Throughout, `<tenant>` is the env's tenant and `<env>` is the **short**
environment name (e.g. tenant `acme`, env `prod` → `terraform-acme/prod/`,
namespace `acme-prod`). The worked paths below use `acme` / `prod`.

## What this is not

- It does **not** supply the Cloudflare token or apply anything. The token is
  supplied only in the runtime env at apply time; the apply itself is
  `erun terraform apply` (see below) and `erun deploy`.
- It does **not** build the runtime image — that is `erun-build-env`, which
  produces the `<tenant>-devops` Dockerfile for a custom runtime image (swapped in
  via `imageOverrides.erun-devops`). Platform component **charts** install **by
  reference** from the published registry (`erun deploy … --components …`), so they
  are never baked. The **Terraform tree** is different: it is a repo file tree that
  `erun terraform apply` reads from the project root, and a sourceless runtime env
  has no worktree — so for the apply to find it in the runtime env, the tree must be
  **baked into the runtime image** via `erun-build-env`'s `/opt/erun/release`
  convention (it surfaces at `~/git/<tenant>/`). Agent envs read it from the
  worktree and bake nothing.
- It does **not** own the runtime `<tenant>-devops` chart — `erun-build-env` does.
  The runtime pod is a universal per-env concern (every tenant has one, platform or
  not). A tenant that ships the platform components this skill produces runs them on
  its own version line, so its runtime must too: `erun-build-env` publishes a
  `<tenant>-devops` chart at the tenant version (its Step 6, **required** for such a
  tenant), and `erun deploy` rejects deploying tenant components against the shared
  `erun-devops` runtime. A bootstrap/erun-only env that deploys no components of its
  own may still ride the shared `erun-devops` chart (image swapped via
  `imageOverrides.erun-devops`). Either way, never add a runtime umbrella under
  `<tenant>-devops/k8s/` **from this skill** — the runtime chart is
  `erun-build-env`'s to own; adding one here would shadow it.

## Step 1 — resolve the erun version and registry to pin to

Everything references erun at one version, so the edge and charts match the
platform the env will run.

```sh
if [ -n "${ERUN_TENANT:-}" ]; then
    # Inside a deployed env: the running version is the one to pin. The first
    # line is "erun <version> (<commit> built <date>)" — take the 2nd field;
    # --no-registry skips the remote version lookup.
    erun_version=$(erun version --no-registry | head -n 1 | awk '{print $2}')
else
    # On a laptop: pull the value out of the env's "runtimeversion: <ver>" line.
    erun_version=$(grep 'runtimeversion' ~/.config/erun/<tenant>/<environment>/config.yaml | awk '{print $2}')
fi
ref="v${erun_version}"          # Terraform module ref + Helm chart version
```

The container registry defaults to `ghcr.io/sophium`; if the env records a
`runtimeregistry`, or marks a DEPLOY registry in `containerregistries`, use that
instead. Call it `<registry>` below.

## Step 2 — the Terraform tree

Terraform runs **inside the per-env folder**: it picks up the symlinked
`common.tf` (providers + shared locals, canonical at the root) and adds that
env's services via the folder's own `main.tf`, with values from `<env>.tfvars`.
The tenant modules in `modules/` wrap erun's published modules.

```
terraform-acme/
├── common.tf                       # providers + shared locals — CANONICAL
├── variables.tf                    # shared variable declarations — CANONICAL
├── .gitignore                      # .terraform/, *.tfplan, *.tfstate*
├── modules/
│   └── terraform-acme-cluster-edge/ # wraps erun's terraform-erun-cluster-edge
│       ├── main.tf
│       ├── variables.tf
│       └── outputs.tf
└── prod/                           # one folder per env
    ├── common.tf      -> ../common.tf      (symlink)
    ├── variables.tf   -> ../variables.tf   (symlink)
    ├── main.tf                     # THIS env's services
    └── prod.tfvars                 # env-specific values
```

There is **no `run.tf`** and **no per-env `apply.sh`/`setup.sh`/`confirm.sh`** —
`erun terraform apply` provides that workflow (Step 4).

**`terraform-acme/common.tf`** (canonical; symlinked into each env):

```hcl
terraform {
  required_version = ">= 1.3"
  # Required so `erun terraform init -backend-config=path=` persists the state
  # location to plan/apply. erun points it at ~/.erun/terraform/<tenant>/<env>/
  # on the durable home PVC; without this block Terraform silently keeps state in
  # ./terraform.tfstate inside the (read-only) playbook tree and apply fails there.
  backend "local" {}
  required_providers {
    kubernetes = { source = "hashicorp/kubernetes", version = "~> 2.30" }
    helm       = { source = "hashicorp/helm", version = "~> 2.17" }
  }
}

# In a runtime pod these use the in-cluster service account; on a laptop, KUBECONFIG.
provider "kubernetes" {}
provider "helm" {
  kubernetes {}
}
```

Run `erun terraform init <tenant> <env>` once on a **writable** checkout (e.g. `<tenant>-local`) after scaffolding: it downloads providers and generates `.terraform.lock.hcl` covering `linux/amd64` + `linux/arm64`. **Commit that lock** — a read-only runtime env can only `init` from a committed lock (it runs `-lockfile=readonly`), and one cross-arch lock initializes on any env. Re-run `init` and re-commit after changing providers.

**`terraform-acme/variables.tf`** (canonical; symlinked into each env). The
Cloudflare token is **not** a tfvar — it is a secret injected at apply time as
`TF_VAR_cloudflare_api_token` from the env's `CLOUDFLARE_API_TOKEN`:

```hcl
variable "cloudflare_api_token" {
  type      = string
  sensitive = true
}
variable "acme_email" { type = string }
variable "services_zone" { type = string }
```

**`terraform-acme/modules/terraform-acme-cluster-edge/main.tf`** — the tenant
module that wraps erun's published module, pinned to `<ref>`:

```hcl
variable "cloudflare_api_token" {
  type      = string
  sensitive = true
}
variable "acme_email" { type = string }
variable "services_zone" { type = string }

module "edge" {
  source = "git::https://github.com/sophium/erun.git//erun-devops/terraform-erun/modules/terraform-erun-cluster-edge?ref=v1.0.0"

  cloudflare_api_token = var.cloudflare_api_token
  acme_email           = var.acme_email
  services_zone        = var.services_zone
}
```

Substitute the real `?ref=v<erun_version>` from Step 1. Add `variables.tf` /
`outputs.tf` that pass the module's inputs and outputs through. As erun
publishes more platform modules (e.g. `terraform-erun-cloudflare-services`),
add a sibling tenant module under `modules/` the same way.

**`terraform-acme/prod/main.tf`** — instantiates the tenant module(s) for this
env:

```hcl
module "cluster_edge" {
  source = "../modules/terraform-acme-cluster-edge"

  cloudflare_api_token = var.cloudflare_api_token
  acme_email           = var.acme_email
  services_zone        = var.services_zone
}
```

**`terraform-acme/prod/prod.tfvars`** — env-specific values (no secrets):

```hcl
services_zone = "services.erunpaas.com"
acme_email    = "ops@erunpaas.com"
```

Create the env folder's `common.tf` and `variables.tf` as **relative symlinks**
to the root, so every env runs identical providers + var declarations:

```sh
mkdir -p terraform-acme/prod
ln -s ../common.tf    terraform-acme/prod/common.tf
ln -s ../variables.tf terraform-acme/prod/variables.tf
```

### Secrets Terraform produces

The Cloudflare token above is a secret going **in** — the operator already holds
it and injects it at apply time. A credential Terraform **creates** goes the
other way: an SES SMTP password, a generated database user, an API key for a
provisioned service. It must not leave through an operator's clipboard, and it
must not be pasted into `<env>.tfvars` or a `values.<env>.yaml`. Terraform writes
it to **AWS Secrets Manager** at a tenant/env-scoped path, and a sync in the
cluster materialises it as a **Kubernetes Secret** in the tenant's namespace:

```
terraform apply
  └─ creates the credential (e.g. aws_iam_access_key → ses_smtp_password_v4)
  └─ writes it to AWS Secrets Manager at  <tenant>/<env>/<name>
cluster
  └─ a sync materialises it as a Kubernetes Secret in <tenant>-<env>
  └─ the workload consumes the Secret; it never sees Secrets Manager
```

One authoritative store, rotatable in place, and nobody reads the value to
retype it somewhere else.

**Path convention: `<tenant>/<env>/<name>`.** Two things depend on it. A
secret's owner is legible from its name without opening it, and an IAM policy
can scope the reader to the prefix — which is what makes the bootstrap
credential below narrow rather than account-wide. Derive it in `locals`, never
inline it per resource:

```hcl
locals {
  secret_prefix = "${var.tenant}/${var.environment}"   # e.g. acme/prod
}
```

**The module shape.** The resource pair, and outputs that **name** the secret
rather than emitting it:

```hcl
resource "aws_secretsmanager_secret" "ses_smtp" {
  name = "${local.secret_prefix}/ses-smtp"
}

resource "aws_secretsmanager_secret_version" "ses_smtp" {
  secret_id = aws_secretsmanager_secret.ses_smtp.id
  secret_string = jsonencode({
    host     = "email-smtp.${var.region}.amazonaws.com"
    port     = "587"
    username = aws_iam_access_key.ses.id
    password = aws_iam_access_key.ses.ses_smtp_password_v4
  })
}

output "ses_smtp_secret_name" { value = aws_secretsmanager_secret.ses_smtp.name }
output "ses_smtp_secret_arn"  { value = aws_secretsmanager_secret.ses_smtp.arn }
```

Never add an output carrying the material. `sensitive = true` only redacts the
CLI's own rendering — the value is still in `terraform output -json` and still
in state (see The credential is still in state, below).

**erun's clusters are not EKS, so IRSA is unavailable.** State this before
reaching for a sync, because it invalidates the pattern almost every AWS
document describes. There is no EKS OIDC provider to associate a ServiceAccount
with — an erun cluster is a self-managed node, or OrbStack behind Tailscale —
and the API server's discovery document is not publicly reachable, so the
cluster cannot be registered as an IAM OIDC provider either. Anything beginning
"annotate the ServiceAccount with `eks.amazonaws.com/role-arn`" cannot work
here. Do not scaffold it and do not suggest it.

Nor is the pod's own AWS identity a substitute. erun does place one in the
runtime pod — the `erun-host` profile in `~/.aws/credentials`, refreshed from
the operator's alias — and that is what `erun terraform apply` authenticates
with. It is **short-lived** and lives on that env's home PVC. A controller that
must re-read Secrets Manager hours later cannot use it.

**erun installs no sync — the operator installs one, in this tree.** No erun
chart and no published erun Terraform module deploys External Secrets Operator,
the Secrets Store CSI driver, or any equivalent; `terraform-erun-cluster-edge`
installs Traefik, cert-manager and the DNS-01 shim and nothing else. The one
credential erun itself puts in an env's namespace is `erun deploy`'s
`<release>-cloudflare` Secret, minted from the env's Cloudflare alias — that is
Cloudflare-specific and deploy-time, not a general sync. So the sync is a tenant
module, installed by `helm_release` exactly as the edge module installs
cert-manager. External Secrets Operator (ESO) is the reference choice:

```hcl
resource "helm_release" "external_secrets" {
  name             = "external-secrets"
  repository       = "https://charts.external-secrets.io"
  chart            = "external-secrets"
  version          = "<pin>"        # pin explicitly, like every other dependency
  namespace        = "external-secrets"
  create_namespace = true
  set {
    name  = "installCRDs"
    value = "true"
  }
}
```

**The bootstrap credential.** With IRSA unavailable, the sync authenticates with
one long-lived IAM access key for a dedicated user that can do nothing but read
this tenant/env prefix:

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": ["secretsmanager:GetSecretValue", "secretsmanager:DescribeSecret"],
    "Resource": "arn:aws:secretsmanager:<region>:<account>:secret:acme/prod/*"
  }]
}
```

The trailing `*` is required, not laziness: AWS appends a six-character suffix
to every secret ARN, so `…:secret:acme/prod/ses-smtp-AbCdEf` is what the policy
must match. `…:secret:*` is a different thing and is not acceptable. Do **not**
grant `secretsmanager:ListSecrets` — it is not resource-scopable, so granting it
hands the key a directory of every secret in the account. Name each secret
explicitly in the `ExternalSecret` (`remoteRef`/`extract`, never a `find`) and
the policy never needs it.

Hold the key in a single Kubernetes Secret in the tenant's namespace, and point
a **namespaced `SecretStore`** at it — not a `ClusterSecretStore`, which would
let any namespace in the cluster read through this credential:

```yaml
apiVersion: external-secrets.io/v1beta1   # newer ESO serves external-secrets.io/v1
kind: SecretStore
metadata: { name: acme-prod-aws, namespace: acme-prod }
spec:
  provider:
    aws:
      service: SecretsManager
      region: eu-west-1
      auth:
        secretRef:
          accessKeyIDSecretRef:     { name: acme-prod-sm-reader, key: access-key-id }
          secretAccessKeySecretRef: { name: acme-prod-sm-reader, key: secret-access-key }
---
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata: { name: acme-prod-ses-smtp, namespace: acme-prod }
spec:
  refreshInterval: 1h
  secretStoreRef: { name: acme-prod-aws, kind: SecretStore }
  target: { name: acme-prod-ses-smtp, creationPolicy: Owner }
  dataFrom:
    - extract: { key: acme/prod/ses-smtp }
```

Apply the two CRs through a **tiny local chart** installed by `helm_release`
with `depends_on` the ESO release — not `kubernetes_manifest`, which needs the
CRD to exist at *plan* time and therefore fails on a first apply that installs
ESO in the same run. This is the same reason erun's own cluster-edge module
ships its Issuer as `chart-issuer/` instead of a manifest resource; copy that
shape rather than rediscovering it.

Name the trade rather than pretending it away: this is **one narrow, rotatable,
auditable credential in place of many scattered ones** — a reduction in
exposure, not an elimination of it. It is also the one credential this pattern
cannot distribute, since it is the bootstrap: mint it out of band and apply the
Secret once.

**Rotation — three owners, and the third is the one that gets missed.**

- *Terraform* owns the credential. `terraform apply -replace='module.mail.aws_iam_access_key.ses'`
  mints a new key and writes a new Secrets Manager version in one apply. The
  path does not change, so nothing downstream is re-wired. Note that
  `aws_iam_access_key` is destroyed before it is recreated, so the old key stops
  working the moment the apply runs — sequence the workload restart with the
  apply, don't leave it for later.
- *The sync* owns propagation. ESO's `refreshInterval` is the upper bound on how
  long a rotated value takes to reach the cluster; set it deliberately.
- *The workload* owns pickup. A Secret consumed as environment variables
  (`envFrom`, `valueFrom.secretKeyRef`) is captured at container start and never
  changes in a running pod — rotation needs `kubectl rollout restart`. A Secret
  mounted as a volume is updated in place by the kubelet, but that only helps if
  the process re-reads the file. Decide which at design time and write the
  restart into the rotation procedure where it is needed. A secret that cannot
  be rotated without someone remembering an undocumented redeploy is a secret
  nobody rotates.

**The credential is still in Terraform state — this pattern does not fix that.**
If Terraform creates the IAM key, the material is in state whatever else happens
to it; writing it to Secrets Manager adds a copy, it does not remove one. This
solves **distribution and rotation**, not state exposure. On erun that is
concrete: `common.tf`'s `backend "local" {}` keeps state at
`~/.erun/terraform/<tenant>/<env>/` on the env's home PVC, unencrypted, so
anyone who can `erun open` that env can read every credential Terraform has ever
created there.

Pick one of the two mitigations **per credential, in the scaffold** and
record which in a comment in the module's `main.tf`. Leaving it undecided is
not an option:

1. **Terraform creates the identity, not the credential.** Manage the
   `aws_iam_user` and its policy; mint the access key out of band and write the
   Secrets Manager version out of band too (`aws secretsmanager put-secret-value`),
   so Terraform manages the secret container but never its material. Nothing
   secret enters state. This is the same stance the blueprint already takes for
   the Zitadel masterkey, so it is a consistent extension rather than a new
   idea — **prefer it wherever the credential allows it.** The cost is honest:
   rotation becomes a manual out-of-band re-put, not an `apply`.
2. **Treat the state backend as secret material** — encrypt it and
   access-control it. On erun's default local backend that means the env's home
   PVC and the set of people who can `erun open` the env. If that set is wider
   than the set who may hold the credential, this mitigation is not met: either
   move state to a backend that encrypts at rest with a restrictive key policy,
   or take option 1.

**Option 1 is unavailable for a provider-derived credential** — one the
provider computes as a local transform on the managed resource itself, rather
than returning it from an API. `aws_iam_access_key.ses_smtp_password_v4` is
the worked example: it is a SigV4 transform of the secret access key that the
AWS provider derives on the resource, not a value the IAM API returns, so no
`data` source can yield it for a key minted out of band — that holds even
under `terraform import`, since there is nothing on the API side to import it
from. This is easy to mis-check: a plural `aws_iam_access_keys` data source
does exist, but it returns access-key *metadata* only, and never the secret or
its SMTP derivation, so it cannot substitute. For this class of credential,
Terraform must own the resource that derives it, and the material enters
state regardless of preference. Hand-deriving the transform to keep the key
out-of-band anyway is not a workaround for this — don't do it.

**The mitigation is a per-credential decision, not a per-tree one.** A tree
with one credential that cannot take option 1 still applies option 1 to every
other credential that can — don't abandon the pattern tree-wide because one
resource requires the fallback. The common shape mixes both in the same
module set: an IAM reader key for the ESO sync (see erun installs no sync,
above) takes option 1 — Terraform creates the `aws_iam_user` only, the key is
minted and applied out of band — while a provider-derived key like the SMTP
password takes option 2, in the same tree.

**When the fallback is forced, the comment must say why, not just which.**
The "record which, in a comment" rule above still applies verbatim when
option 1 is chosen freely. When a credential falls back to option 2 because
option 1 is structurally unavailable, name the reason at the resource —
the derivation, and the absence of a data source that could substitute:

```hcl
# ses_smtp_password_v4 is a local SigV4 derivation the AWS provider computes
# on this resource; no data source can yield the secret material or its SMTP
# derivation, so this key cannot be minted out of band (option 1). Falls back
# to option 2: state is encrypted + access-controlled (see Secrets Terraform
# produces, above).
resource "aws_iam_access_key" "ses" {
  user = aws_iam_user.ses.name
}
```

A comment that names the reason lets a reviewer tell a considered fallback
from an unconsidered one; "state backend is encrypted" with no reason attached
does not.

## Step 3 — the Helm side (optional — the patch/override path)

`erun deploy` installs the published component charts **by reference** from the
env's config selection, so you need these umbrella charts only for real-time
patching or a per-env chart-value override the config can't express. When you do,
each platform component is a thin umbrella chart under
`<tenant>-devops/k8s/<tenant>-<component>/` — the directory name, the `Chart.yaml`
`name:`, and the Helm release are all `<tenant>-<component>` (e.g. `acme-docs`,
`acme-backend-api`) — that **depends on** erun's published OCI chart
`erun-<component>` (never a copy), with its **per-env values** beside it. Name the
directory for the tenant, not for the erun chart it wraps: `erun deploy` keys the
component name off the directory, so `acme-docs/` deploys as `acme-docs` (wrapping
`erun-docs`), not as `erun-docs`.

Component selection is **opt-in**: `erun deploy` rolls out exactly the charts you
name via `--components` (or a saved default selection persisted in the env's
`deploy.components`), and with no selection deploys the environment's runtime
chart alone (bootstrapping/healing it). It never deploys a component just because
its chart directory is present — so a chart that should not run yet simply stays
unselected.

**The values-file contract (this is what fails if you skip it).** For every env
it deploys, `erun deploy <tenant> <env>` reads `values.<env>.yaml` from **each**
chart directory — it is **required, per-chart, with no fallback** to a base
`values.yaml` and no config-dir overlay. A chart dir with a `Chart.yaml` but no
`values.<env>.yaml` fails the whole deploy at spec resolution:
`values file not found for environment "<env>": …/<component>/values.<env>.yaml`.
So every umbrella chart needs a `values.<env>.yaml` for **every** env it will be
deployed to.

**That includes the agent env.** `<tenant>-local` is not only the authoring
workbench — the desktop composes build→push→deploy for it (on create and via the
Deploy button), so deploying it resolves these same charts. Scaffold
`values.local.yaml` **and** `values.<runtime-env>.yaml` (e.g. `values.prod.yaml`)
for every component, or `erun deploy <tenant> local` fails on the first missing
one. (If a component genuinely should not run in an env, exclude it from that
deploy via `--components`; it still needs a values file for the envs it does run
in.)

So each component gets three files:

```yaml
# <tenant>-devops/k8s/acme-backend-api/Chart.yaml   (dir name == chart name == <tenant>-<component>)
apiVersion: v2
name: acme-backend-api
version: 0.1.0
dependencies:
  - name: erun-backend-api
    version: "1.0.0"                  # the erun release version from Step 1
    repository: "oci://ghcr.io/sophium/charts"
```

```yaml
# <tenant>-devops/k8s/acme-backend-api/values.local.yaml
# Subchart values nest under the dependency name. Forward tenant + environment
# (see note below); the published chart carries every other default.
erun-backend-api:
  tenant: <tenant>          # e.g. acme
  environment: <env>        # the short env name, e.g. local
```

```yaml
# <tenant>-devops/k8s/acme-backend-api/values.prod.yaml
erun-backend-api:
  tenant: <tenant>
  environment: <env>        # e.g. prod
  # add genuinely prod-specific overrides here
```

Do this for **every** platform component you deploy — one `<tenant>-<component>`
umbrella dir per wrapped erun chart. This is the closed set of the erun
platform's components (`erun-backend-api`, `erun-backend-postgres`,
`erun-backend-db`, `erun-powerdns`, `erun-zitadel`, `erun-docs`) — **never** the runtime
`erun-devops` chart (see What this is not). E.g. `acme-backend-api/` wraps
`erun-backend-api`. Keep each values file to genuinely env-specific
overrides; the published charts carry the defaults.

**Track `Chart.lock`, ignore the built `charts/`.** Resolving an umbrella's
dependency produces two things: `Chart.lock` (the pinned, reproducible
resolution — **commit it**) and `charts/<subchart>-<version>.tgz` (the
downloaded subchart — a build artifact). `erun deploy` runs `helm dependency
build` for any umbrella chart before it installs, so `charts/` is rebuilt from
`Chart.lock` on every deploy and never needs to be committed. Add the artifact
to the repo's `.gitignore` so it doesn't land as an untracked file:

```gitignore
# Helm dependency build artifacts — rebuilt from Chart.lock by `erun deploy`.
**/charts/*.tgz
```

Commit the tgz (vendor it) only for an air-gapped install where the OCI registry
is unreachable at deploy time.

**Forward `tenant`/`environment` into each subchart.** helm does not pass
top-level values into a wrapped subchart, so a subchart that `{{ required }}`s
them (`erun-backend-api`, `erun-docs`) reads them in its **own** scope. Author
them nested under the subchart key in each `values.<env>.yaml`, as above — and
`erun deploy` satisfies the subchart on either path: a **worktree** deploy `-f`s
that file directly, and a **by-reference** deploy (a runtime env with no
worktree) both re-scopes deploy's threaded `--set`s under the subchart key *and*
`helm pull`s the published chart to `-f` this same bundled file. So a wrapped
subchart no longer fails with `tenant is required` by reference — but keep the
nested values: the worktree path depends on them, and the by-reference path
applies them. Components that don't require them (`erun-backend-postgres`,
`erun-backend-db`, `erun-powerdns`) can use `{}` — but forward them anyway if the
component reads them for labels/config. A comment-only file is still valid for
the latter; the file just has to exist so `helm -f` resolves.

**Deploy only env-appropriate components.** Selection is opt-in and per env, so a
component that can't run in an env is simply left unselected — never forced. Use
the umbrella (directory) names, `<tenant>-<component>`, in `--components` or the
saved `deploy.components`. Two real cases: `<tenant>-powerdns` binds `:53` via
`hostNetwork` and uses a private base image (`erun-powerdns:<pin>`), so it belongs
in the runtime env with a ghcr pull secret, not a local agent env (orbstack has
neither); `<tenant>-docs` is a no-op locally. For a local platform, select the
backend trio (`--components <tenant>-backend-postgres,<tenant>-backend-db,<tenant>-backend-api`);
add `<tenant>-powerdns` only where `:53` + the pull secret exist. `<tenant>-zitadel`
is the hosted IdP and needs three things the cluster must already have — a
32-character masterkey in an existing Secret (named to it as
`zitadel.masterkeySecretName`; erun never generates one), a public DNS record for
the platform's auth host, and a cert for it from the edge's DNS-01 Issuer
(`zitadel.certManagerIssuer`) — so it belongs in the runtime env after the edge,
never in a local agent env. The runtime
chart deploys on its own with an empty selection, so a bare `erun deploy <tenant>
<env>` bootstraps or heals the runtime without touching the components.

**Set the PowerDNS bind address per env, before deploying.** `<tenant>-powerdns`
binds `:53` on the node via `hostNetwork`, at `powerdns.localAddress`. Leave it
empty to bind the node's own interface IP — the default on current erun,
injected via the downward API, which coexists with a node's systemd-resolved
`127.0.0.53:53` stub. Binding `0.0.0.0` collides with that stub on any node that
runs one, so `pdns_server` can't bind and the pod CrashLoops. Pin it in the
umbrella's `values.<env>.yaml` up front rather than hand-patching a failed
deploy — a literal node IP where you must (a multi-homed node, or an older erun
pin whose default is still `0.0.0.0`), or `0.0.0.0` only where `:53` is free:

```yaml
# <tenant>-devops/k8s/acme-powerdns/values.prod.yaml
erun-powerdns:
  powerdns:
    localAddress: ""      # empty → node interface IP; a literal IP pins one bind
```

The override is honored on every erun-powerdns version; only the empty-value
default differs (node IP on current erun, `0.0.0.0` on pre-hostIP pins).

**Every** erun chart — the runtime `erun-devops` chart *and* every component
chart (`erun-powerdns`, `erun-zitadel`, `erun-backend-*`, `erun-docs`) — publishes under the
registry's `/charts` path, kept separate from the same-named image repo
(`<registry>/<component>`) so a chart never collides with its image at the same
ref. So every dependency `repository` is `oci://<registry>/charts`, and every
dependency `version` is pinned to the erun release from Step 1.

## Step 4 — how it gets applied (not in this skill)

The operator applies these in the runtime env, never by hand-running `terraform`
or `kubectl`:

- **Platform account (once, up front).** A Terraform tree that stands up the
  cluster edge or other cluster-scoped platform resources runs in-pod as the
  runtime ServiceAccount, which by default holds namespaced admin only. Make the
  platform env a **platform account** — `erun init --platform-account` (or
  `platformaccount: true`) — so `erun deploy` binds its SA to `cluster-admin`;
  the first such deploy must run from an admin-capable context. Without it the
  apply is denied (`namespaces is forbidden` / `customresourcedefinitions … is
  forbidden`).
- **Terraform:** `erun terraform init` once (downloads providers, records the
  committed cross-arch lock), then `erun terraform apply` resolves
  `terraform-<tenant>/<env>/` (or `<tenant>-devops/terraform-<tenant>/<env>/` — the
  same `-devops` convention `build`/`deploy` use, so a tree baked under
  `<tenant>-devops/` needs no `paths.terraform` override) from the active cloud
  context and runs `fmt → plan → confirm → apply` (no implicit init), injecting
  `TF_VAR_cloudflare_api_token` from `CLOUDFLARE_API_TOKEN`. State and the provider
  cache live on the durable home directory (`~/.erun/terraform/<tenant>/<env>/` on
  the `/home/erun` PVC), off the read-only playbook tree, so they survive a pod
  restart — this relies on the `backend "local" {}` block in `common.tf`. The
  confirm step prompts the operator to **type the environment name** before
  applying, so changes can't land in the wrong env. Preview with
  `erun terraform apply --dry-run`.
- **Helm:** `erun deploy --version <version> --components=…` (with the `values.<env>.yaml`
  overlay), then the `erun-enable-hosting-edge` skill / `erun terraform apply`
  for the edge.

## Step 5 — validate the scaffold

```sh
# Terraform: formatting + per-env structure are valid (init needs network for the module).
terraform -chdir=terraform-acme/prod fmt -check -recursive ..
terraform -chdir=terraform-acme/prod validate || true   # validate after `init` resolves the module

# Symlinks resolve and point at the canonical root files.
readlink terraform-acme/prod/common.tf      # -> ../common.tf
readlink terraform-acme/prod/variables.tf   # -> ../variables.tf

# Produced credentials are referenced, never carried: the tree names secrets and
# never holds their material, and no reader policy is wildcarded past its prefix.
grep -rn 'ses_smtp_password_v4\|secret_string' terraform-acme/*/*.tfvars 2>/dev/null \
  && echo "REFUSE: produced credential in tfvars"
grep -rn 'secretsmanager:\*\|"secret:\*"\|ListSecrets\|ClusterSecretStore\|role-arn' terraform-acme/ \
  && echo "REVIEW: over-broad Secrets Manager reader, or an IRSA annotation that cannot work here"

# Helm umbrellas: dependencies resolve from OCI, and every chart has a values
# file for every env it deploys to (missing one fails erun deploy — see below).
for c in <tenant>-devops/k8s/*/; do
  [ -f "$c/Chart.yaml" ] || continue
  helm dependency build "$c"
  for env in local prod; do   # every env this platform deploys to
    [ -f "$c/values.$env.yaml" ] || echo "MISSING: $c/values.$env.yaml"
  done
done
```

Commit the tree with `git` so the team shares the wiring. These are repo files,
not deliverables — they belong in the git worktree, not `${ERUN_OUTPUTS_DIR}`.

## Maintenance, repair & upgrade

This skill owns the artifacts for their whole life, not just day one. **If
`terraform-<tenant>/` or any `<tenant>-<component>` umbrella already exists, do
not stop — enter maintenance mode** and reconcile the existing tree to the
target erun version in place. First scaffold and ongoing maintenance are the
same skill.

**Target version.** Same resolution as Step 1: the env's `runtimeversion`
(which moves with `erun upgrade`), or an explicit version the operator names.
One erun version pins every reference on both sides — the Terraform `?ref` and
every Helm chart `version` — so bump them together, never piecemeal.

**Detect, then reconcile against this skill's own contract:**

- **Repair gaps** — fill what the contract requires but the tree lacks: absent
  `common.tf`/`variables.tf` symlinks, a missing per-env `values.<env>.yaml`
  (including `values.local.yaml` for the agent env), a missing
  `**/charts/*.tgz` entry in the repo `.gitignore`, an uncommitted
  `Chart.lock`. Add only what's missing; never clobber the project's own
  content (env-specific tfvars, values overrides, extra tenant modules).
- **Reconcile produced-secret wiring** — where the tree creates a credential,
  hold it to Secrets Terraform produces: a path that isn't `<tenant>/<env>/…`,
  a reader policy widened past its prefix (or granted `ListSecrets`), a
  `ClusterSecretStore` where a namespaced one belongs, an IRSA annotation that
  cannot work on these clusters, an output that emits material, or no recorded
  state decision. Narrow the policy and fix the wiring in place; a credential
  found in a tfvars or values file is a **rotation**, not an edit — flag it,
  because deleting the line does not un-leak it.
- **Upgrade the pins** — re-pin **every** erun reference to `<ref>`/`<version>`:
  the terraform module `source = "…?ref=v<version>"` in each
  `modules/terraform-<tenant>-*/main.tf`, and each `<tenant>-<component>`
  umbrella `Chart.yaml` dependency `version:`.
- **Refresh derived artifacts** — after re-pinning, run `helm dependency update`
  on each umbrella to regenerate `Chart.lock` + `charts/` for the new versions,
  and commit the updated `Chart.lock`. (`helm dependency build` only rebuilds
  `charts/` from an *in-sync* lock and errors on a stale one — it fits a fresh
  scaffold with no lock and `erun deploy`'s pre-install step, not a re-pin.) Then
  re-apply terraform (`erun terraform apply`, Step 4) so the tree carries the
  upgraded module.
- **Clean up what the reconcile supersedes** — after previewing, remove only what
  no longer belongs: a `<tenant>-<component>` umbrella dir dropped from the plan
  (repo side), and in the env's namespace a deployed release the new set replaces —
  a hand-deployed release whose name doesn't match the `<tenant>-<component>`
  convention (e.g. `erun-backend-api`, now owned by the umbrella as
  `acme-backend-api`), or a component release no longer selected (`helm
  --kube-context <ctx> -n <ns> list` finds them). Uninstall a superseded release so
  the new one can adopt its resources — **but only when it is stateless.** A
  stateful release (postgres, or anything owning a data PVC) must not be casually
  `helm uninstall`ed: that can delete the PVC and the data with it (a chart-templated
  PVC — like postgres's — is removed on uninstall). Preserve the volume
  (retain it, let the new release adopt it) or **stop and flag it for explicit
  operator action** — never drop data, `/home/erun`, or a Secret as a side effect of
  a rename. When in doubt, leave it and report it.

**Idempotent, in-place, preview first.** Safe to re-run; edit files where they
live; **show the diff/plan before writing** — the pin changes and the files
you'd add — and let the operator confirm. Touch only version pins and genuine
gaps; never rewrite working project content, and never regenerate a file just
to reformat it.

**Confirm the tenant on a loose match.** If the existing `terraform-<tenant>/`
looks unrelated to the tenant you were asked about, confirm before reconciling
rather than assuming.

Maintenance does not relax the exclusions: still **never** wrap the runtime
`erun-devops` chart or add an `erun-devops`/`<tenant>-devops` umbrella (see What
this is not), and the runtime chart still deploys on its own with an empty
selection.

## Error behaviour

| Failure mode | Recovery |
|---|---|
| `terraform-<tenant>/` already exists | Not a stop — enter maintenance mode (see Maintenance, repair & upgrade): reconcile and upgrade the existing tree in place — re-pin every erun reference to the target version, fill gaps against the contract, refresh derived artifacts — after showing the diff/plan first. Offer to add a new `<env>/` folder if that's the ask. Confirm the tenant first only when the match looks unrelated. |
| The erun version can't be resolved (no `erun version`, no `runtimeversion`) | Stop and ask which erun version to pin to. Never default to `main` for production wiring — the edge and charts must match the deployed platform. |
| `terraform-erun-cluster-edge?ref=v<ver>` doesn't resolve on `terraform init` | The version has no matching git tag yet. Confirm the erun release exists; pin to the latest released `vX.Y.Z`. |
| `helm dependency build` 404s on the OCI chart | That version's chart isn't published. `erun push`/`erun build --release` publishes image + chart together — pin to a version that has been pushed. |
| `erun deploy` fails: `values file not found for environment "<env>": …/<component>/values.<env>.yaml` | That umbrella chart has no per-env values file for the env being deployed. Create `<component>/values.<env>.yaml` (an empty/comment-only file is valid). Remember the agent env: `values.local.yaml` is required too, since the desktop deploys `<tenant>-local`. |
| Operator asks to put the Cloudflare token in `<env>.tfvars` | Refuse. The token is a secret injected as `TF_VAR_cloudflare_api_token` from `CLOUDFLARE_API_TOKEN` at apply time — it must not be committed. |
| Operator asks to put a Terraform-**produced** credential (an SES SMTP password, a generated DB password, a provisioned API key) in `<env>.tfvars`, a `values.<env>.yaml`, or a chart value | Refuse, and say where it goes instead: Terraform writes it to AWS Secrets Manager at `<tenant>/<env>/<name>` and a sync materialises it as a Kubernetes Secret in the tenant's namespace (see Secrets Terraform produces). The tree carries the secret's **name**, never its material. Same refusal for an output that emits the value, and for pasting it into a console field by hand. |
| Operator asks for IRSA / a `eks.amazonaws.com/role-arn` ServiceAccount annotation so the sync can reach Secrets Manager | Refuse — it cannot work. erun's clusters are not EKS, there is no OIDC provider to associate the ServiceAccount with, and the API server's discovery document is not publicly reachable, so the cluster cannot be registered as one either. Use the namespaced `SecretStore` with a bootstrap credential scoped to `secretsmanager:GetSecretValue` on `<tenant>/<env>/*`. |
| An `erun-devops`/`<tenant>-devops` umbrella appears under `<tenant>-devops/k8s/` | That is the runtime chart — `erun-build-env`'s concern, not this skill's. Don't create or edit it here. A `<tenant>-devops` chart is legitimate (and **required** once the tenant ships its own components, so the runtime rides the tenant version line) and owned by `erun-build-env`; leave it to that skill. Only remove a stray one this skill created by mistake — `erun deploy` matches the runtime release name and would otherwise install a duplicate as the runtime chart. |

## Important

- **Give the repo root agent guidance.** If the repository root has no
  `AGENTS.md`/`CLAUDE.md`, also apply the `erun-blueprint-agents` skill so any
  agent — or human — landing in the repo gets erun-environment orientation.
- **Reference, never copy.** Terraform modules are sourced from erun's GitHub by
  `?ref=v<version>`; Helm charts are OCI dependencies pinned to the version. If
  you find yourself pasting erun's module or chart contents into the tenant
  tree, stop — that defeats the contract.
- **`common.tf` is canonical at the root and symlinked into each env.** Do not
  copy it per env; all envs must run identical providers and shared locals.
  Env-specific resources go in the env folder's own `main.tf`.
- **No `run.tf`, no per-env shell scripts.** `erun terraform apply` owns the
  apply workflow.
- **Every umbrella chart needs a `values.<env>.yaml` for every env it deploys
  to — including `local`.** erun deploy requires it per-chart with no fallback,
  and the desktop deploys the `<tenant>-local` agent env. A missing one fails the
  whole deploy at spec resolution.
- **Pin one erun version across both sides.** The Terraform `?ref`, the Helm
  chart `version`, and the env's `runtimeversion` move together; bump them
  together after an `erun upgrade`.
- The Cloudflare token never lives in the tree. It is supplied in the runtime
  env and injected at apply time.
- **A credential Terraform produces reaches the cluster through Secrets Manager,
  never through a person.** Write it to `<tenant>/<env>/<name>`, sync it into a
  Kubernetes Secret, and keep only the secret's *name* in the tree. **IRSA is
  unavailable** — these are not EKS clusters — so the sync authenticates with a
  bootstrap key scoped to `secretsmanager:GetSecretValue` on that prefix, and
  erun ships no sync of its own: the tenant module installs one. Decide, and
  record, how the credential is kept out of Terraform state.
