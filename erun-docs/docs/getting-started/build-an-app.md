---
title: Build a small app
---

# Build a small app with ERun

End-to-end walkthrough — from an empty directory to a running service in your env, in roughly ten minutes.

<figure className="erun-hero-figure">
  <img src="/img/build-app-flow.svg" alt="Six-step overview of building a small app with ERun. Step 1: create the project (mkdir, git init, VERSION). Step 2: bootstrap ERun (erun init --bootstrap). Step 3: ask the Agent to add a service via a skill — the Agent loads the matching skill and writes the source, Dockerfile, and chart by hand. Step 4: build and deploy (erun build --deploy). Step 5: see it running (kubectl port-forward and curl). Step 6: iterate." />
</figure>

The whole point of ERun's conventions is that you don't hand-write Dockerfiles, helm charts, or deploy plans by trial and error. **ERun ships [skills](/concepts/skills)** — guidance bundles that teach the Agent how to lay out a service, write a conformant Dockerfile, structure the helm chart, and wire the deploy plan. You describe the component; the Agent reads the matching skill and writes the pieces idiomatic for your project.

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

The Agent loads the `go-service` [skill](/concepts/skills) (deployed automatically when you opened the env) and reads its SKILL.md — the layout, the multi-stage Dockerfile pattern, the helm chart structure, the deploy-plan rule. It then writes the source + Dockerfile + chart by hand, adapted to what you described (HTTP, the route, the stub return value):

```
hello-erun/
├── api/                              ← new source module
│   ├── go.mod
│   └── cmd/api/main.go               ← stub returns "hello from erun"
└── hello-erun-devops/
    ├── docker/api/                   ← new Docker context
    │   └── Dockerfile                ← multi-stage Go pattern from the skill
    └── k8s/api/                      ← new helm chart
        ├── Chart.yaml
        └── templates/
            ├── deployment.yaml
            └── service.yaml
```

It also appends `api` to the deploy plan in `.erun/config.yaml`. Everything's hand-written by the Agent following the skill's guidance — no code generator. You see the diff in your editor and approve before it lands.

If your project has its own preferences (a specific HTTP framework, a house style, a custom audit annotation), drop a project skill under `<repo>/.erun/skills/go-service/` to layer your guidance on top of the built-in — the Agent picks up both on the next env open. See [Skills spec](/agent-reference/skills-spec) for the format.

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

The conventional pieces don't need to change — only the source. Subsequent edits to the Dockerfile or chart are rare; when you do need them, edit them by hand. The skill stays available; ask the Agent to re-read it when you're unsure about the convention.

## Built-in skills

ERun ships skills for the common patterns: `go-service`, `node-service`, `python-service`, `java-service`, `static-site`, `migration-job`, `cron-job`, `add-ingress`. Each is deployed automatically into the env and discoverable by the Agent's skill loader. Each one follows [Conventions](/concepts/conventions). The full catalogue + the SKILL.md format + the project-layering rules live on [Skills spec](/agent-reference/skills-spec).

## What you just did

In ten minutes you:

- Bootstrapped an ERun-aware project from a blank directory.
- Asked the Agent to add a service. ERun's `go-service` skill taught the Agent the layout, the Dockerfile pattern, and the chart structure; the Agent wrote the source + Dockerfile + chart + deploy-plan entry by hand from that guidance.
- Built and deployed the service into a real Kubernetes namespace.
- Iterated end-to-end with one command.

You didn't write a Dockerfile. You didn't write a helm chart. You didn't write a deploy plan. ERun's job is to make the conventional pieces conventional — so you spend your time on what's actually different about your service.

## Where next

- **[Conventions](/concepts/conventions)** — what the templates conform to.
- **[Three scenarios](/getting-started/three-scenarios)** — apply the same flow to peer review, hotfix, CI wait.
