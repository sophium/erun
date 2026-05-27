---
title: Cheatsheet
---

# Cheatsheet

Daily ERun commands and common patterns.

## Lifecycle

```bash
# Universal entry point — runs init or open depending on state.
erun

# Explicit forms.
erun init <tenant> <env>           # set up a new tenant or env
erun open <tenant> <env>           # attach to an existing env
erun delete <tenant> <env>         # tear down

# Always safe to preview.
<any command> --dry-run            # show every action without executing
```

## Building and shipping

```bash
# Agent env workflow.
erun build                          # build the current context with a snapshot tag
erun push                           # rebuild + push to registry
erun deploy                         # build → push → helm upgrade

# Combined.
erun build --deploy                 # build then deploy in one shot
erun build --release                # stable tag — typically run by CI

# Runtime env workflow.
erun deploy --version 1.0.77 --tenant t --environment prod \
            <component>             # roll out a pre-built version
```

## Inspecting

```bash
erun list                           # tenants · envs · effective target
erun list cloud                     # managed cloud contexts only
erun doctor                         # diagnose local config + runtime pod
erun version                        # build version + commit
```

## Editing and attaching

```bash
erun open                           # shell into the env
erun open --vscode                  # launch VS Code Remote-SSH
erun open --intellij                # launch IntelliJ Gateway
erun open --no-shell                # print kubectl/cd commands for scripting
```

## MCP for agents

```bash
# From the desktop, port-forward is published automatically.
cat <UserConfigDir>/erun/portforward/mcp/<tenant>/<env>.json

# Wire your agent client:
claude --mcp-config <that-file>
# or: codex --mcp-server-url http://127.0.0.1:<port>/mcp
```

## Common patterns

### Spin up a sibling env for peer review

```bash
git worktree add ~/code/myapp-review feature-from-peer
erun init myapp review --project-root ~/code/myapp-review
erun open myapp review
```

### Hotfix while keeping the feature env alive

```bash
git worktree add ~/code/myapp-hotfix hotfix/urgent
erun init myapp prod-local --project-root ~/code/myapp-hotfix \
                            --kubernetes-context erun-prod
erun open myapp prod-local
# inside the pod:
erun build --release
erun deploy <component> --tenant myapp --environment prod --version 1.0.77
```

### Run integration tests against the env's own stack

```bash
erun open myapp local
# inside the pod:
./test/integration.sh
```

### Switch the env's registry to ECR

```bash
erun init myapp rihards-dev \
  --container-registry 020362606330.dkr.ecr.eu-west-2.amazonaws.com \
  --kubernetes-context erun-004-020362606330-eu-west-2
```

### Override a `build` step with a project script

Drop `build.sh` (or `push.sh`, `deploy.sh`, `release.sh`) at the project root or in any module subtree. ERun finds it and runs it instead of the built-in command. See [Conventions](/concepts/conventions#command-overrides-via-commandsh).

## Read this when something's wrong

- **[Troubleshooting](/reference/troubleshooting)** — common errors and how to diagnose.
- **`erun doctor`** — first command to run when anything's unclear.
- **`<command> --dry-run`** — preview before you commit.
