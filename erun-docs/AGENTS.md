# AGENTS.md

Module-specific guidance for `erun-docs`. Follow the repository root `AGENTS.md` first, then apply this file for work in this subtree.

## Module role

- `erun-docs` is the public product documentation site for ERun, served at `https://docs.erunpaas.com`.
- It is a Docusaurus 3.x app: TypeScript config, Yarn workspaces-style layout, local search plugin (no Algolia), Mermaid for diagrams.
- This module owns the documentation **content** and the Docusaurus build configuration. It does not contain application behavior.
- The deploy contract — image, helm chart, and CI wiring — lives outside this module:
  - Image: `erun-devops/docker/erun-docs/`
  - Helm chart: `erun-devops/k8s/erun-docs/`
  - CI workflow: `.github/workflows/erun-docs.yml`

## Hosting contract

- The static site is published to Cloudflare Pages via `wrangler pages deploy` running inside a Kubernetes Job (the chart above).
- The Job is wired as a Helm `post-install,post-upgrade` hook with `helm.sh/hook-delete-policy: before-hook-creation,hook-succeeded`. Each `erun deploy` runs a fresh Job; success cleans it up.
- Cloudflare Pages handles CDN, TLS, custom domain (`docs.erunpaas.com`), and per-deploy preview URLs. There is no in-cluster Service, Ingress, or long-running pod.

## One-time external setup

These must exist before the first deploy succeeds. They are external to this repository:

1. **Cloudflare Pages project** named `erun-docs` (Direct Upload type — do **not** connect a Git source, the Job uploads directly).
2. **Custom domain** `docs.erunpaas.com` attached to the Pages project.
3. **Cloudflare API token** with `Pages:Edit` scope, plus the Cloudflare account id.
4. **Kubernetes Secret** named `cf-creds` (configurable via `docs.credentialsSecretName`) in the target tenant/env namespace, with keys:
   - `CLOUDFLARE_API_TOKEN`
   - `CLOUDFLARE_ACCOUNT_ID`
5. **DNS** for `erunpaas.com` proxied through Cloudflare so the custom domain serves TLS automatically.

The Job will refuse to run if `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ACCOUNT_ID`, `CF_PAGES_PROJECT`, or `CF_PAGES_BRANCH` are missing.

## Local development

```bash
cd erun-docs
yarn install
yarn start          # http://localhost:3000
yarn build          # produces ./build/
yarn typecheck      # tsc against docusaurus.config.ts and sidebars.ts
```

Requires Node 20+. The repo pins `yarn@1.22.22` via `packageManager`.

## Content rules

- **Voice.** User-facing voice (someone using the product). Engineering-internal rules belong in the other `AGENTS.md` files, not in published docs.
- **Headings.** Sentence case (`Tenants and environments`, not `Tenants And Environments`).
- **Examples.** Every CLI snippet must be a command that actually works against the current ERun release. Out-of-date commands are worse than no commands — prefer a TODO marker if a command is unstable.
- **Links.** Use Docusaurus relative routes (`/concepts/tenants-and-environments`), not raw file paths.
- **Mermaid.** Enabled. Use it for state machines and architecture sketches; don't draw what a screenshot would show better.
- **Frontmatter.** Every doc page declares at least `title:`. Use `slug:` only when the file path doesn't match the desired URL.

## Adding a page

1. Drop a markdown file under `docs/<section>/<page>.md` with a `title:` frontmatter.
2. Add the page id to `sidebars.ts` under the right category (file id is the path relative to `docs/` without `.md`).
3. Run `yarn start` and verify the page renders + the navbar entry shows up.
4. Run `yarn build` to catch broken links — `onBrokenLinks: 'throw'` will fail the build on any.

## Versioning

- Versioning is **off** initially. Turn it on when ERun ships its first GA release.
- When turned on, use Docusaurus's `docusaurus docs:version <X.Y>` command to snapshot the current `docs/` into `versioned_docs/version-X.Y/`.

## What not to do

- Do not import images of Architecture or Privacy-sensitive infra screens. Stick to abstract diagrams.
- Do not embed API tokens or environment-specific URLs in example commands.
- Do not add a separate README under `erun-docs/` — the site's `intro.md` and this `AGENTS.md` together cover the audiences (users and contributors).
