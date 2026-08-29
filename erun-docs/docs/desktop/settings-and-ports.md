---
title: Settings and ports
---

# Settings and ports

- **Settings in one place.** Runtime sizing, AI tooling configuration, port mappings, SSH keys, and cloud bindings — all editable from one screen per environment.

### See and manage an environment's public address from Ports

For an environment whose project is a [platform deployment](/concepts/networking#platform-service-exposure), the Ports tab's **Public access** section lists every service exposed at a public hostname — a lock icon shows whether it's served over HTTPS, with buttons to copy the address and open it in your browser. **Expose a service** adds one without a terminal: name the service, the address traffic to this environment already reaches, and the port to route to — the same [`erun expose`](/cli/expose) primitive underneath. **Remove public access** takes every exposed service's address down at once behind a confirmation naming how many it affects; there's no narrower per-service removal. A project that isn't a platform deployment says so instead of showing an empty list, and a listing you don't have permission to see says that too.

## Where next

- [Working with an Agent](/desktop/working-with-an-agent) — the AI tooling configuration this same Settings screen carries.
- [`erun expose`](/cli/expose) — the same public-address actions from the CLI.
