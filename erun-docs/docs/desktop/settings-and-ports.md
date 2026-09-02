---
title: Settings and ports
---

# Settings and ports

- **Settings in one place.** Runtime sizing, AI tooling configuration, port mappings, SSH keys, and cloud bindings — all editable from one screen per environment.

### See and manage an environment's public address from Ports

For an environment whose project is a [platform deployment](/concepts/networking#platform-service-exposure), the Ports tab's **Public access** section lists every Service the environment is actually running (the same discovery [`erun services`](/cli/expose#listing-services) does) instead of a name you have to already know. Each row shows its ports and its state: already exposed (with the real `scheme://hostname`, a lock icon for HTTPS, and buttons to copy/open it), exposable (pick it, pick a port if it has more than one, see the hostname it will get before you commit, then expose it — the same [`erun expose`](/cli/expose) primitive underneath, defaulting the target IP to `127.0.0.1` for a local cluster), or not exposable yet (named as such, not offered as an action — its Service name doesn't follow the naming convention `erun expose` routes by). **Remove public access** takes every exposed service's address down at once behind a confirmation naming how many it affects; there's no narrower per-service removal. A project that isn't a platform deployment says so instead of showing an empty list, and a listing you don't have permission to see says that too.

## Where next

- [Working with an Agent](/desktop/working-with-an-agent) — the AI tooling configuration this same Settings screen carries.
- [`erun expose`](/cli/expose) — the same public-address actions from the CLI.
