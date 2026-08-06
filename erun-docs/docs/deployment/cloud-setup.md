---
title: Cloud setup
---

# Cloud context setup (admin)

This page is for whoever sets up shared cloud capacity for a team. For the day-to-day Operator view, see [Cloud contexts](/concepts/cloud-contexts).

## What a managed cloud context is

A managed cloud context is a single cloud VM that ERun **provisions for you** and runs k3s on. Environments deploy into it, and ERun starts and stops it around your working day so it doesn't bill around the clock. ERun owns the VM's whole lifecycle — you don't hand it a pre-existing cluster.

If you already have a Kubernetes cluster — EKS, GKE, an on-prem k3s — you don't need a managed cloud context at all: bind an environment straight to its kubeconfig context with `erun init --kubernetes-context <name>` (see [Bring your own cluster](#bring-your-own-cluster) below). Managed cloud contexts exist for the common case where you want ERun to own the VM and its cost.

## Prerequisites

1. An AWS account reachable through IAM Identity Center (SSO). Managed cloud contexts are AWS-only today.
2. Permission for your SSO permission set to create and manage EC2 instances, security groups, and IAM instance profiles in the target region.
3. An OIDC issuer for ERun API callers — derived automatically from your AWS web-identity token in Step 1.

## Step 1 — Register a cloud provider alias

```bash
erun cloud init aws
```

This writes an AWS SSO profile to `~/.aws/config`, opens a browser login, and saves a cloud provider alias (`<user>+<account>@aws`) plus the OIDC issuer the deployed ERun APIs trust. See [`erun cloud`](/cli/cloud).

## Step 2 — Provision the cloud context

```bash
erun context init --alias me+020362606330@aws --region eu-west-2 --dry-run   # preview the AWS plan
erun context init --alias me+020362606330@aws --region eu-west-2
```

`erun context init` creates a security group, an IAM role + instance profile, **launches an EC2 instance** (default `c8gd.2xlarge`, 100 GB gp3), installs k3s, and writes the kubeconfig context locally. The instance bills while running — Step 4 covers stopping it. See [`erun context`](/cli/context).

## Step 3 — Bind an environment

```bash
erun init my-tenant rihards-dev \
  --type=remote-agent \
  --kubernetes-context <context-name-from-step-2> \
  --container-registry 020362606330.dkr.ecr.eu-west-2.amazonaws.com
erun cloud set my-tenant rihards-dev --alias me+020362606330@aws
```

`erun cloud set` ties the environment to the provider alias (the desktop's env edit modal does the same).

## Step 4 — Start, stop, verify

```bash
erun context list                      # contexts + live status
erun context stop  <context>           # pause compute billing
erun context start <context>           # bring it back (gated to working hours; --force overrides)
```

`erun open` against a bound environment brings the context up if it's stopped, and ERun stops it again when the environment goes idle (see [idle stop](/concepts/cloud-contexts#idle-stop)). To keep a context up while you debug a problem, `erun context disable-api-stop <context>` locks it against every stop path until you `enable-api-stop` it again.

## Common admin tasks

| Task | How |
|---|---|
| Provision a new context | `erun context init --alias <alias>` |
| Stop / start a context | `erun context stop` / `erun context start <context>` |
| Keep a context up during repair | `erun context disable-api-stop <context>`, then `enable-api-stop` after |
| Rotate the OIDC issuer | `erun cloud oidc --alias <alias>` |
| Re-inject an environment's host AWS credentials | `erun cloud refresh <tenant> <env>` |
| See status / which contexts exist | `erun context list` |

## Acting as your AWS identity in an environment {#host-credentials}

Attaching an AWS alias to an environment is what lets its runtime pod reach AWS **as you** — pull from your ECR, read your S3 buckets, call Bedrock. There is no separate toggle: ERun writes short-lived credentials derived from your alias into the pod's `~/.aws/credentials` under the `erun-host` profile, and the runtime pod's `AWS_PROFILE` points at it.

Those credentials are **temporary**, on the order of an hour, and nothing in the cluster renews them. Two things keep an environment usable:

- **The desktop app** refreshes them on a timer for every open environment.
- **`erun open`** refreshes them as you open the environment. If your SSO session has lapsed the refresh is skipped with a warning and the shell still opens — the environment keeps whatever credentials it already had.

For anything else — a long-running session, a script, an Agent that never reopens the environment — refresh them explicitly:

```bash
erun cloud refresh my-tenant rihards-dev
```

Nothing sensitive passes through the caller: ERun reads your own AWS profile and streams the credentials to the pod on stdin, so no key or token lands in a command line or a transcript. See [`erun cloud refresh`](/cli/cloud#cloud-refresh).

When credentials do go stale, the symptom usually appears far from the cause — a container pull failing with `no basic auth credentials`, or an SDK call reporting `ExpiredToken`. Ask ERun instead of guessing:

```bash
erun doctor my-tenant rihards-dev
```

For an environment with an AWS alias, `doctor` reports whether the `erun-host` profile is present, when it expires, and which AWS region the environment resolves — see [Troubleshooting](/reference/troubleshooting#host-credentials-expired).

## Bring your own cluster

To use an existing cluster instead of a managed context, skip `erun context init` and bind the environment to its kubeconfig context:

```bash
erun init my-tenant rihards-dev --type=remote-agent --kubernetes-context my-existing-eks
```

ERun deploys into it exactly the same way, but doesn't manage its lifecycle — there's no `erun context start` / `stop` / idle-stop for a cluster ERun didn't provision.

## Cloudflare aliases (DNS and zone management)

Cloudflare support is a **separate alias type**, not a managed cloud context. There is no VM to provision and no lifecycle to manage — a Cloudflare alias simply delivers a delegated, account-scoped API token into an environment's runtime pod so in-pod tooling (such as Terraform) can manage Cloudflare DNS and zones. It is independent of the AWS path above: an environment can hold an AWS alias and a Cloudflare alias at the same time.

The token is **delegated** — you mint it once in the Cloudflare dashboard as a **Custom Token** scoped to what you'll do with it. For DNS and zone delegation that's two rows, `Zone → Zone → Edit` (create the delegated subzone) and `Zone → DNS → Edit` (manage records), with **Zone Resources: Include → All zones**. If you'll also deploy static sites with the same token, add `Account → Cloudflare Pages → Edit` (an Account-scope row, so also set **Account Resources: Include → your account**); add more rows (Workers, R2, …) for anything else the token will manage. ERun never asks for your dashboard password or your Global API Key. Register it with the guided setup:

```bash
erun cloud init cloudflare
```

ERun walks you through it: it shows the token page and the exact permissions, waits while you create the token, takes it masked and verifies it against the Cloudflare API, then **auto-resolves the account ID from the token** and proposes a label. In the desktop app, the **Cloudflare** button under **Settings → Cloud aliases** runs this same flow in a terminal — the same way the AWS button runs `erun cloud init aws`. The token is stored in a local secret store referenced from your config — the raw token is **never written into `erun-config.yaml`** — and the alias is named `<token-name>+<account>@cloudflare`. See [`erun cloud`](/cli/cloud).

Attach it to an environment the same way you attach an AWS alias — and because aliases are routed by provider type, this **coexists with** any AWS alias the environment already carries:

```bash
erun cloud set my-tenant rihards-dev --alias dns-edit+0a1b2c3d@cloudflare
```

When that environment's runtime pod comes up, ERun injects the token as `CLOUDFLARE_API_TOKEN` and the account as `CLOUDFLARE_ACCOUNT_ID`. The in-pod Terraform Cloudflare provider reads both natively, so an Agent can manage DNS without ever handling the credential itself.

### Enabling the public hosting edge

The injected token is what the **`terraform-erun-cluster-edge`** module (in `erun-devops/terraform-erun/modules/`) applies to stand up the public HTTPS edge: it installs a Traefik ingress controller and cert-manager, and creates a **namespaced DNS-01 `Issuer`** (Cloudflare, using the token + the platform config's `acmeemail`) that issues a wildcard certificate for `*.<services-zone>`. The `erun-enable-hosting-edge` skill drives it end to end (apply + verify the issuer and wildcard cert). Wiring `erun expose`'s Ingress to that issuer and hosting the console behind the edge are the remaining follow-ups.

### Delegating the services zone to PowerDNS

For a platform that serves its services zone from its own PowerDNS singleton (the [`platform:` block](/reference/configuration#platform-block)), the parent domain must delegate that subdomain to the platform's nameservers. The **`terraform-erun-services-delegation`** module (in `erun-devops/terraform-erun/modules/`) writes that delegation into the Cloudflare parent zone: one **NS record** per platform nameserver for `<services-zone>`, plus the **glue A records** those in-zone nameservers need. A tenant consumes it by `?ref=v<version>` from a thin `terraform-<tenant>-services-delegation` wrapper, the same way it wraps `terraform-erun-cluster-edge`, passing the services zone, the platform nameservers, and the cluster's public authoritative IP. This is the parent-side counterpart to the child zone the `erun-powerdns` chart bootstraps — together they make the platform authoritative for `*.<services-zone>` end to end, with the delegation under Terraform rather than cut by hand.
