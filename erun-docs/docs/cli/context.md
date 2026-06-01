---
title: erun context
---

# `erun context`

Create and power **managed cloud contexts**. A managed cloud context is an EC2 instance running k3s that ERun provisions for you — environments deploy into it. It **bills while running**, so the lifecycle is: create once, then start and stop around your working day.

## Synopsis

```
erun context init [flags]
erun context list
erun context start CONTEXT [--force]
erun context stop CONTEXT
erun context disable-api-stop CONTEXT
erun context enable-api-stop CONTEXT
```

## Subcommands

### `context init`

Provisions a new context: creates a security group, an IAM role + instance profile, **launches an EC2 instance and installs k3s via cloud-init**, waits for it to come up, and writes the kubeconfig context locally. The instance bills until you `stop` it.

| Flag | Description |
|---|---|
| `--alias` | Cloud provider alias to launch under (prompted if omitted). |
| `--context` | Context name (auto-generated if omitted). |
| `--region` | Cloud region (default `eu-west-2`). |
| `--instance-type` | EC2 instance type (default `c8gd.2xlarge`). |
| `--disk-size` | Root disk GB (default `100`). |
| `--disk-type` | Root disk type (default `gp3`). |
| `--subnet-id`, `--security-group-id`, `--key-name` | Optional EC2 overrides. |

### `context list`

Lists configured contexts and refreshes each one's live status from AWS (a read call — no changes).

### `context start` / `context stop`

Start or stop the EC2 instance and wait for it to reach the target power state. `start` also re-attaches the instance profile and rewrites the kubeconfig with the new public IP. `stop` pauses compute billing (the disk still bills).

`start` is gated by **working hours** — outside the configured window it refuses, so an idle-stopped context doesn't get woken by accident. `--force` overrides the gate.

### `context disable-api-stop` / `context enable-api-stop`

Lock or unlock the instance against being stopped (the AWS `DisableApiStop` attribute). `disable-api-stop` keeps an unhealthy context up while you repair it — it blocks the idle monitor, the desktop Stop button, and auto-stop alike. `enable-api-stop` restores normal stop behaviour.

## Examples

```bash
erun context init --alias me+123456789012@aws --dry-run   # preview the AWS plan
erun context start erun-001-...-eu-west-2
erun context stop erun-001-...-eu-west-2
erun context disable-api-stop erun-001-...-eu-west-2       # while debugging
```

## Error behaviour

| Failure | Behaviour |
|---|---|
| Alias not configured. | Aborts before any AWS call (even under `--dry-run`). |
| Unsupported region / instance type / disk size. | Rejected before launch. |
| Context name already exists (`init`). | Errors; does not relaunch. |
| `start` outside working hours without `--force`. | Refuses with the window in the message; no instance change. |
| Context has no instance ID (`start`/`stop`/`*-api-stop`). | Errors; nothing changes. |

Managed cloud contexts are an additive AWS feature — see [Cloud setup](/deployment/cloud-setup) for the model and [idle stop](/concepts/cloud-contexts) for auto-stop.
