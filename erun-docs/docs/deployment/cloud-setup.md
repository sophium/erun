---
title: Cloud setup
---

# Cloud context setup (admin)

This page is for whoever runs cloud infrastructure for a team. Operators reading the rest of these docs don't need it — they reference cloud contexts by alias once a cluster admin has declared them. If you're an operator, see [Cloud contexts](/concepts/cloud-contexts) instead.

## What a cloud context is, concretely

A cloud context = a managed Kubernetes cluster ERun can start, stop, and connect to. From ERun's point of view, two records describe it:

- A **cloud provider** (`ERunConfig.cloudproviders[]`) — the identity and account that owns the cluster.
- A **cloud context** (`ERunConfig.cloudcontexts[]`) — the cluster itself: cluster id, region, instance shape, and the lifecycle controls ERun uses to start/stop it.

Both live in the per-user `~/.config/erun/config.yaml` for every operator who wants to use the cluster. Admins typically publish the lines for the team and operators paste them in (or set them via `erun init` flags / the desktop edit modal).

## Prerequisites

Before declaring the records:

1. **A Kubernetes cluster.** EKS, GKE, AKS, OpenShift, k3s — anything ERun can reach with a kubeconfig and standard RBAC.
2. **A way to start and stop the cluster cheaply.** ERun's idle-stop assumes you can wake the cluster on demand:
   - **AWS:** the cluster's node group sits behind an ASG that scales to zero; an `aws ec2 start-instances` (or EKS Karpenter / managed node group autoscaler) brings it back.
   - **GCP / Azure:** equivalent autoscaler with min size zero.
   - **On-prem / k3s:** typically left running; idle-stop is a no-op.
3. **An OIDC identity provider for ERun callers.** The same trusted issuer your operators sign in with (Identity Center, Auth0, Keycloak, …). See [Sign-in](/agent-reference/api-protocol#sign-in-oidc).
4. **An IAM/RBAC binding that grants ERun's caller permission to start, stop, and describe the cluster.** Minimum AWS example for EKS:

   ```jsonc
   {
     "Effect": "Allow",
     "Action": [
       "eks:DescribeCluster",
       "ec2:DescribeInstances",
       "ec2:StartInstances",
       "ec2:StopInstances",
       "ec2:DescribeInstanceStatus"
     ],
     "Resource": "*"
   }
   ```

   Restrict `Resource` to the specific cluster + instance ARNs in production.

## Step 1 — Declare the cloud provider

In `~/.config/erun/config.yaml`:

```yaml
cloudproviders:
  - alias: MyOrg                       # short, human-readable
    provider: aws                      # aws | gcp | azure | onprem
    account: "020362606330"             # account / project / subscription id
    profile: default                    # AWS named profile (optional)
    sso:
      startUrl: https://myorg.awsapps.com/start
      region: eu-west-2
    oidc:
      issuerUrl: https://issuer.example.com/oauth2/default
      audience: erun-api
```

`alias` is what every cloud context references — pick something short and stable. Operators see this alias in their `erun list cloud` output.

## Step 2 — Declare the cloud context

```yaml
cloudcontexts:
  - alias: MyOrg+020362606330@aws         # by convention: <provider-alias>+<account>@<provider>
    providerAlias: MyOrg                   # references cloudproviders[].alias above
    region: eu-west-2
    clusterId: erun-004-020362606330-eu-west-2
    instance:
      type: t3.large                       # EC2 / GCE instance type
      count: 1
      ami: ami-0fedcba9876543210            # optional pinned AMI
    idle:
      defaultTimeout: 30m                   # cluster idle policy default
      workingHours: "09:00-19:00"
      timezone: Europe/London
    autoscaling:
      minNodes: 0                            # zero means full stop on idle
      maxNodes: 4
```

`alias` here is what `EnvConfig.cloudprovideralias` (and the desktop's binding picker) references.

## Step 3 — Bind an env to the cloud context

Once the records exist, an Operator binds an env to the context with `erun init`:

```bash
erun init my-tenant rihards-dev \
  --remote \
  --kubernetes-context erun-004-020362606330-eu-west-2 \
  --container-registry 020362606330.dkr.ecr.eu-west-2.amazonaws.com
```

Or set `EnvConfig.cloudprovideralias: "MyOrg+020362606330@aws"` in the desktop's env edit modal.

## Step 4 — Verify

```bash
erun list cloud
```

Should report the cloud context with status `stopped` initially. `erun open` against the bound env will start it, attach, and stop it again when the idle policy fires.

## Common admin tasks

| Task | How |
|---|---|
| Add a new cloud context | Append another entry under `cloudcontexts[]` and distribute. |
| Rotate the OIDC issuer | Edit `cloudproviders[].oidc.issuerUrl`. Operators pick up the change next `erun open`. |
| Change the instance type | Edit `cloudcontexts[].instance.type`. Existing pods reschedule on next start. |
| Restrict who can use a context | Set `cloudproviders[].oidc.allowedSubjects` to a list of service-account subjects. |
| Force-stop an idle-blind context | `aws ec2 stop-instances --instance-ids ...` (the desktop sidebar resyncs status). |

## Operator-facing handoff

When the admin work is done, share four pieces of information with operators:

1. The `cloudproviders[]` and `cloudcontexts[]` YAML to paste into their `~/.config/erun/config.yaml`.
2. The OIDC sign-in URL and how to obtain a token (or the service-account credential file for unattended Agents).
3. The container registry endpoint (e.g., the ECR URL).
4. The exact `erun init` command for an env bound to the new context.

Operators paste, `erun init`, `erun open`. The cluster wakes up, the runtime pod rolls out, the env is ready.
