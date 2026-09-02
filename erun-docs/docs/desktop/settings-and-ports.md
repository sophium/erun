---
title: Settings and ports
---

# Settings and ports

- **Settings in one place.** Runtime sizing, AI tooling configuration, port mappings, SSH keys, and cloud bindings — all editable from one screen per environment.

### See and manage an environment's public address from Ports

For an environment whose project is a [platform deployment](/concepts/networking#platform-service-exposure), the Ports tab's **Public access** section lists every service exposed at a public hostname — a lock icon shows whether it's served over HTTPS, with buttons to copy the address and open it in your browser. **Expose a service** adds one without a terminal, and it starts by showing you what there is to expose: a **Service** picker lists every Service the environment is actually running, with its ports, and marks the ones already published with the hostname they answer at. Picking one fills in the rest — the Ingress routes to *that* Service, the public label defaults to its name with the tenant prefix removed, and a single-port Service fills the port in (a multi-port one leaves it for you to choose, rather than guessing between an app port and a metrics port). The label stays editable, since it is what appears in the address. You still supply the address traffic to this environment already reaches (`127.0.0.1` for a cluster running on this machine). It is the same [`erun expose`](/cli/expose) primitive underneath. **Remove public access** takes every exposed service's address down at once behind a confirmation naming how many it affects; there's no narrower per-service removal. A project that isn't a platform deployment says so instead of showing an empty list, and a listing you don't have permission to see says that too — including the Service picker, which degrades to a typed name rather than disappearing when the namespace cannot be read.

## Where next

- [Working with an Agent](/desktop/working-with-an-agent) — the AI tooling configuration this same Settings screen carries.
- [`erun expose`](/cli/expose) — the same public-address actions from the CLI.
