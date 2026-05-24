---
title: Cloud contexts
---

# Cloud contexts

A **cloud context** is ERun's abstraction over a managed cloud Kubernetes cluster (e.g. an AWS EKS cluster). It binds a Kubernetes context to a specific cloud provider account, region, and instance, so the platform can start and stop the underlying compute on demand.

This is what enables ERun to give every developer their own cluster without paying for it 24/7.

## Lifecycle

| Status | Meaning |
|---|---|
| `stopped` | Underlying cloud instance is off. Kubernetes is unreachable. |
| `starting` | ERun has issued a start request; the instance is booting. |
| `running` | Cluster is reachable; environments can be opened. |
| `stopping` | Idle timer fired (or a manual stop) — instance is winding down. |

ERun watches each cloud context in the background and surfaces the current status in `erun list` and in the desktop sidebar.

## Idle policy

For environments backed by a cloud context, an in-pod **idle monitor** tracks:

- Terminal activity (keystrokes / output) on the SSH session.
- Network traffic in the runtime namespace.

When both fall below the configured thresholds for the configured timeout, the linked cloud context is stopped. Open the environment again to bring it back; ERun starts the cloud context and waits for the API server before re-deploying the runtime pod.

## Configuring

You typically don't configure cloud contexts directly — they're declared once per cluster by an administrator and then referenced by alias (e.g. `MyOrg+123456789012@aws`) from each environment that uses them.
