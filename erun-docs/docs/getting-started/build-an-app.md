---
title: Build a small app
---

# Build a small app with ERun

End-to-end walkthrough — from an empty directory to a running service in your env, in roughly ten minutes.

The whole point of ERun's conventions is that you don't hand-write Dockerfiles, helm charts, or deploy plans for routine components. **ERun ships scaffolding skills**, and the Agent uses them on your behalf. You describe the component; ERun generates the conventional pieces.

## Prerequisites

- ERun installed (see [Install](/getting-started/install)).
- `git` on your `PATH`. ERun resolves the project root by walking up to find a `.git` directory; the first step of this tutorial runs `git init`.
- Kubernetes enabled in your Docker tool (Docker Desktop, OrbStack, …) or a cloud context already wired up.
- An Agent (optional). The runtime pod ships the `EnvConfig.aitool` CLI (`claude`, `codex`, …) pre-wired against in-pod MCP — `erun open` brings it up alongside the env. Working without an Agent is fine; the same skills are exposed as CLI subcommands.

## 1. Create the project

```bash
mkdir hello-erun && cd hello-erun
git init
echo '1.0.0' > VERSION
```

## 2. Bootstrap ERun

```bash
erun init hello-erun local --bootstrap --set-default-tenant
```

This creates the per-user / per-project config and scaffolds `hello-erun-devops/` — the conventional DevOps module that holds Dockerfiles, charts, and the deploy plan.

## 3. Add a service — via a skill

Ask the Agent:

> Add a Go HTTP service called `api` that returns "hello from erun" on `GET /`.

The Agent calls ERun's `scaffold` skill (an MCP tool exposed by every env's runtime pod — see [Skills](/mcp/overview#built-in-skills)). It picks the `go-service` template, infers the component name (`api`), and generates the conventional pieces:

```
hello-erun/
├── api/                              ← new source module
│   ├── go.mod
│   └── cmd/api/main.go               ← stub returns "hello from erun"
└── hello-erun-devops/
    ├── docker/api/                   ← new Docker context
    │   └── Dockerfile                ← multi-stage Go template
    └── k8s/api/                      ← new helm chart
        ├── Chart.yaml
        └── templates/
            ├── deployment.yaml
            └── service.yaml
```

It also appends `api` to the deploy plan in `.erun/config.yaml`. None of these files were hand-written — the skill generated each from a template the platform ships.

### Or, from the CLI

If you don't have an Agent in the loop, the same scaffolding runs as a CLI subcommand:

```bash
erun add component api --template go-service
```

## 4. Build + deploy

```bash
erun build --deploy
```

ERun builds `api` per the generated Dockerfile, tags it with a snapshot version, pushes to the registry, then `helm upgrade --install`s the chart. The dry-run trace shows every step:

```
audit: erun build --deploy
trace:   resolved env type = local-agent (snapshot tags)
trace:   building <registry>/api:1.0.0-snapshot-<timestamp>
trace:   pushing per-arch tags
trace:   helm upgrade --install api <tenant>-devops/k8s/api/
result: ok
```

## 5. See it running

```bash
kubectl get pods -n hello-erun-local
# NAME             READY   STATUS    RESTARTS   AGE
# api-79f5b9c64    1/1     Running   0          15s
# erun-devops-7c8  3/3     Running   0          2m

kubectl port-forward -n hello-erun-local svc/api 8080:8080
# Another shell:
curl http://localhost:8080
# → hello from erun
```

Or use the [Ingress pattern](/concepts/networking#exposing-application-services) so the service has a stable URL.

## 6. Iterate

Change the message. One command rolls the new build out:

```bash
erun build --deploy
```

The conventional pieces don't need to change — only the source. Subsequent changes to the Dockerfile or chart are rare; when you do need them, edit by hand or ask the Agent to call the `scaffold` skill with `--rewrite` to regenerate from the template.

## Built-in scaffolding templates

ERun ships templates for `go-service`, `node-service`, `python-service`, `java-service`, `static-site`, `migration-job`, and `cron-job`. The Agent picks based on what you describe; the CLI picks via `--template`. Each template follows [Conventions](/concepts/conventions). For the per-template input/output spec (exact files generated, validation rules), see the canonical [`scaffold` skill spec](/mcp/overview#scaffold--generate-conventional-artefacts) or [`erun add`](/cli/add).

## What you just did

In ten minutes you:

- Bootstrapped an ERun-aware project from a blank directory.
- Asked the Agent to scaffold a service. ERun's skill generated source, Dockerfile, helm chart, and deploy plan from a template.
- Built and deployed the service into a real Kubernetes namespace.
- Iterated end-to-end with one command.

You didn't write a Dockerfile. You didn't write a helm chart. You didn't write a deploy plan. ERun's job is to make the conventional pieces conventional — so you spend your time on what's actually different about your service.

## Where next

- **[Conventions](/concepts/conventions)** — what the templates conform to.
- **[Three scenarios](/getting-started/three-scenarios)** — apply the same flow to peer review, hotfix, CI wait.
