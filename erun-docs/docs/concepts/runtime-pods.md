---
title: Runtime pods
---

# Runtime pods

Opening an environment deploys a **runtime pod** into the target Kubernetes namespace. The pod is the unit of work — it carries your repository checkout, a Docker-in-Docker daemon, an SSH server, and an MCP endpoint.

## What runs in the pod

| Container | Role |
|---|---|
| `erun-devops` | Main shell + tools (`erun`, `docker`, `kubectl`, `helm`, `gh`, …). Reads `DOCKER_HOST=unix:///var/run/docker.sock`. |
| `erun-mcp` | MCP server for AI-assisted development. Exposes structured tools (`idle`, `doctor`, `list`, `version`, `raw`). |
| `erun-dind` | Docker daemon (sidecar). Backs `/var/run/docker.sock`. |

All three share two persistent volume claims: `/home/erun` (your workspace and config) and `/var/lib/docker` (the daemon's image store, so builds are cache-warm across pod restarts).

## Why a single pod

A single pod gives one cohesive workspace per environment:

- File edits in `erun-devops` are immediately visible to the daemon in `erun-dind`.
- The MCP container can inspect the same `/home/erun` filesystem the shell sees.
- Cluster RBAC for the pod's ServiceAccount is the single authorization surface for all in-environment tooling.

## Idle / auto-stop

Non-local pods participate in an idle policy: after a configurable timeout of no terminal activity and below a network-traffic threshold, the linked cloud context shuts down. The next `erun open` brings it back. See [Cloud contexts](/concepts/cloud-contexts).
