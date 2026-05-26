---
title: erun add
---

# `erun add`

Generate conventional ERun artefacts from built-in templates. `erun add` is the CLI front-end to the same [`scaffold` MCP skill](/mcp/overview#scaffold--generate-conventional-artefacts) the Agent uses — pick whichever fits your workflow.

## Synopsis

```
erun add <kind> <name> [flags]
```

Today `<kind>` is **`component`**. More kinds are planned (`tenant`, `env-template`).

## `erun add component`

Scaffold a deployable component: source module + Dockerfile + helm chart + deploy-plan entry, all following [Conventions](/concepts/conventions).

```bash
erun add component api --template go-service
```

Generates:

```
<projectRoot>/
├── api/                                ← new source module
└── <tenant>-devops/
    ├── docker/api/                     ← new Docker context
    │   └── Dockerfile
    └── k8s/api/                        ← new helm chart
        ├── Chart.yaml
        └── templates/
            ├── deployment.yaml
            └── service.yaml
```

It also appends `api` to `environments.<env>.k8s.deployments` in `.erun/config.yaml` (idempotent — re-running won't duplicate).

## Flags

| Flag | Description |
|---|---|
| `--template <name>` | Required. One of the built-in templates (see below). |
| `--description "<text>"` | Free-text hint. Seeds comments + picks reasonable defaults (port number, route name, etc.). |
| `--rewrite` | Overwrite existing files. Default behaviour aborts if any target file exists. |
| `--dry-run` | Print what would be created without writing anything. |

## Built-in templates

`go-service`, `node-service`, `python-service`, `java-service`, `static-site`, `migration-job`, `cron-job`. Each template's generated artefacts conform to [Conventions](/concepts/conventions). For the per-template input/output spec (what each generates, exact file list, validation), see the canonical [`scaffold` skill spec](/mcp/overview#scaffold--generate-conventional-artefacts).

## Examples

Scaffold a Go HTTP service from inside the project root:

```bash
erun add component api --template go-service \
  --description "tiny HTTP service that returns hello on GET /"
```

Preview without writing:

```bash
erun add component worker --template go-service --dry-run
```

Re-generate an existing component (overwrite local edits):

```bash
erun add component api --template go-service --rewrite
```

## Output

```
audit: erun add component api --template go-service
trace:   project root            = /Users/you/code/hello-erun
trace:   tenant / env            = hello-erun / local
trace:   template                = go-service
trace:   would create:
trace:     api/go.mod
trace:     api/cmd/api/main.go
trace:     hello-erun-devops/docker/api/Dockerfile
trace:     hello-erun-devops/k8s/api/Chart.yaml
trace:     hello-erun-devops/k8s/api/templates/deployment.yaml
trace:     hello-erun-devops/k8s/api/templates/service.yaml
trace:   would update:
trace:     .erun/config.yaml (append 'api' to environments.local.k8s.deployments)
result: ok

next steps:
  - review the generated source under api/
  - run `erun build --deploy` to ship it
```

## Error behaviour

`erun add` aborts before touching the filesystem on invalid component name (must be lowercase letters / digits / hyphens, starting with a letter), file conflicts (use `--rewrite` to override), missing devops module (suggests `erun init --bootstrap`), or unknown `--template`. A failed deploy-plan update after files are written is reported as a warning, not a hard abort. Full code table: [Agent reference · CLI flag spec · `erun add` error codes](/agent-reference/cli-flags#erun-add).

After scaffolding, the generated files are yours. Edit, refactor, commit — the template is a starting point, not a contract.

## Relationship to the MCP `scaffold` skill

`erun add component` and the [`scaffold` MCP skill](/mcp/overview#scaffold--generate-conventional-artefacts) are two front-ends to the same underlying operation. The Operator picks via CLI; the Agent picks via MCP. The audit trail records both as `action: "erun.add.component"` (CLI) or `action: "mcp.scaffold"` (MCP) so the distinction is visible later.
