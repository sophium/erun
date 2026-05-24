---
title: Welcome
slug: /intro
---

# ERun

**Agentic coding from idea to production — with the operator in control.**

ERun is the platform layer beneath agentic coding. It gives every agent an isolated environment, gives every operator a full audit trail and a one-command take-over, and ships production-grade Kubernetes deploys without compromising on compliance or industry best practices.

<figure className="erun-hero-figure">
  <img src="/img/hero-flow.svg" alt="The operator sits above an idea-to-production flow. Solid arrows take an idea through an agent's isolated environment, into review with operators and other agents, and out to production. Dashed arrows from the operator into the Agent and Review steps are labeled 'join · take over' and 'review · approve'." />
  <figcaption>The operator is in the loop at every step. Solid arrows are the work; dashed arrows are the operator stepping in.</figcaption>
</figure>

---

## The four commitments

**Agent-first surface.** Every environment exposes a structured MCP server. `--dry-run` on any command shows the operator exactly what will happen before it runs — transparency and control by default. `AGENTS.md` files in each module encode the rules — read by humans and agents alike.

**Operator in the loop.** The ERun desktop is your control panel — start, stop, and inspect environments; open any of them in a shell, in VS Code, in IntelliJ, or in any Remote-SSH-capable IDE. Claude, Codex, and other AI agents connect to the same environments through MCP, sharing the workspace with you. Work with agents, review what they did, take over, or work alone. → [Operator in the loop](/collaboration/operator-in-the-loop).

**Iteration speed.** Snapshot tags for local iteration; immutable release tags for promotion. Fingerprint cache means fresh clones promote pinned bases without rebuilding. Idle-stop means you don't pay for cloud compute while you sleep.

**Compliance by default.** Compliance isn't a checklist bolted onto the workflow — it's the workflow. Every environment is a controlled, isolated, audited substrate, and the same controls apply whether an agent or an operator is working in it. The result is repeatable, which is what makes the platform fast as well: speed and compliance reinforce each other instead of trading off.

---

## Get started

Inside your project's git repository:

```bash
erun
```

That's it. The bare `erun` command is the universal entry point: the first run picks your repo as a new tenant and scaffolds a `local` environment; the next run brings the isolated Kubernetes runtime up and attaches your shell. To open in an IDE instead, use `erun open --vscode` or `erun open --intellij`.

No flags, no YAML, no kubeconfig. Run as many environments in parallel as your machine can host — one per agent, per feature branch, per teammate.

---

## Where next

- **[Why ERun](/why)** — design principles in detail.
- **[Create your first environment](/getting-started/first-environment)** — a five-minute walkthrough.
- **[Agent collaboration](/collaboration/overview)** — multi-agent workflows via the erun API.

ERun is open source under [github.com/sophium/erun](https://github.com/sophium/erun).
