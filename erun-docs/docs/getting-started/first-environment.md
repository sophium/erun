---
title: First environment
---

# Create your first environment

The fastest way to see ERun work is to run it against your local Kubernetes (Docker Desktop, kind, k3d, or minikube).

## 1. Initialize a tenant and environment

```bash
cd path/to/your/project
erun init my-tenant local
```

This walks you through:

- Picking a tenant name and project root.
- Choosing the Kubernetes context for the `local` environment.
- Choosing the container registry images will be pushed to.

ERun writes its configuration to `~/.config/erun/` and a small project file at `<repo>/.erun/config.yaml`.

## 2. Open the environment

```bash
erun open my-tenant local
```

This brings the environment up in your Kubernetes cluster (deploying a runtime pod into the environment's namespace) and opens a shell inside it. From inside the environment you can run `erun build`, `erun push`, `erun deploy`, etc. against your project.

You can open as many environments at once as your machine can host. Each environment is its own Kubernetes namespace with its own home volume, docker daemon, and MCP endpoint — so a second `erun open my-tenant another-env` runs alongside the first without interference.

## 3. List environments

```bash
erun list
```

You'll see your tenant, the `local` environment, its Kubernetes context, container registry, runtime version, and current state.

## Next

- [Concepts: tenants and environments](/concepts/tenants-and-environments)
- [Local vs non-local environments](/concepts/local-vs-non-local)
