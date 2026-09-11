# AGENTS.md

Follow root guidance. This module owns public product content and Docusaurus
configuration, not repository engineering rules or application behavior.

## Module role and hosting contract

- The site uses Docusaurus, local search, Mermaid, and the checked-in Node/Yarn
  toolchain configuration. Image/chart ownership lives in
  `erun-devops/docker/erun-docs` and `erun-devops/k8s/erun-docs`.
- Publishing is an explicitly enabled Cloudflare Pages upload Job, a fresh Helm
  post-install/post-upgrade hook removed after success. There is no site Service,
  Ingress, or long-running pod in the cluster.
- A Cloudflare alias supplies a token with Pages:Edit (in addition to edge DNS
  permissions). Deploy creates the credentials Secret and passes account ID;
  the Job creates a missing Direct-Upload Pages project then uploads. Explicit
  secret/account overrides support setups without an alias; do not instruct
  alias users to hand-create the obsolete `cf-creds` setup.
- Refuse missing token, account, project, or branch. Custom-domain attachment and
  DNS remain separate external setup; `docs.erunpaas.com` was recorded as planned,
  not live. Verify its deployment state before claiming it works; preview URLs
  are not proof of custom-domain cutover.

## Local development and validation

Use `yarn install`, `yarn start`, `yarn build`, and `yarn typecheck` here;
read package configuration for pinned versions rather than duplicating them.
A public-content change needs a clean build, link/anchor and terminology sweeps,
and rendered review when layout changes.

Static assets can remain cached across hot reload. Restart the dev server and
hard-refresh before diagnosing an unchanged image. Validate edited SVGs with
`xmllint --noout`; Docusaurus does not validate static XML.
Guidance-only edits use root's consistency/reference validation scope.

## Content rules

- Write product-facing prose, sentence-case headings, and commands verified against
  current behavior. Mark uncertain/unimplemented behavior as planned with its issue.
- Every page needs `title` frontmatter; use `slug` only for an intentional URL change.
  Use Docusaurus routes for links, not repository file paths.
- Introduce diagrams with a lead sentence and explain their implication where useful;
  a chapter must not be only a heading and figure. Show concrete examples rather
  than abstract field inventories when introducing a structure.
- Use visuals for relationships that are materially clearer as sequence, hierarchy,
  comparison, convergence, or layered structure. Skip them for a single fact,
  short command, already-structured code, or a trivial box collection.

## Audience: Operator vs Agent

### The split

- Operator pages explain concepts, workflows, commands, and recovery. Schema tables,
  resolution algorithms, JSON-RPC envelopes, error-code catalogues, and protocol
  internals belong in Agent reference.
- Agent-reference pages include exact contracts plus links back to the Operator
  view. Admin pages address cluster administration separately.
- Choose audience/sidebar placement before writing. Getting started, CLI, Desktop,
  collaboration, and Operator reference are Operator-facing; Agent reference owns
  the detailed spec. Curious Operators can follow links into it.

### Operator-page rules

Lead with the value and workflow, using diagrams where useful. Show the necessary
command rather than enumerating options; point to exact reference details when
needed. Describe settings through the product surface, not YAML fields an Agent
should resolve. Finish with “Where next”: the next hands-on page, a relevant
concept, and at most one Operator-reference page.

### Agent-reference rules

Specify complete input/output schemas, numbered algorithms, labelled state-machine
transitions, and status plus machine-readable error codes. “See source” is not a
public specification. Related pages link to one canonical owner instead of
duplicating contracts.

### Companion pages

Non-trivial concepts have a short Operator explanation and a comprehensive
Agent-reference companion, cross-linked in both directions. Keep this audience
pair even when consolidating overlapping pages within one audience. Existing
examples include audit, idle policy, conventions, OIDC, and registry authentication.

## Canonical terminology

The glossary at `concepts/glossary` owns terms. Sweep both `docs/` and
`static/img/`, including SVG titles/text/comments and referring alt text.

| Concept | Use | Avoid |
|---|---|---|
| Human role | Operator | user, human, developer as substitute role names |
| Assistant role | Agent | bot, copilot, the AI |
| Isolated workspace | environment / env | sandbox |
| Kubernetes backing primitive | namespace | env when referring to Kubernetes internals |
| Development mode | agent env | dev env, snapshot env |
| Serving mode | runtime env | non-local env, prod env, snapshot=false env |
| Managed cluster | cluster / cloud context | ERun cluster |

Role names are capitalized as roles; flowing prose can use lowercase Operator.
Compound terms such as agent env, runtime env, agent-driven, and multi-arch remain
lowercase. Literal schema values keep their required casing.

## Spec discipline

Documented behavior is the contract; undocumented behavior is a defect, not an
optional follow-up. Replace vague “ERun handles X” with the actual mechanism in
the proper audience's canonical page. If it cannot be specified confidently,
mark it `(Planned.)` and link the issue.

### Error behaviour

Every command/action/endpoint needs failure, effect (including partial state),
and recovery. Operator pages use plain-language failures/remedies; exact exit
codes, HTTP statuses, and machine codes belong in the reference contract.

### Single source of truth

One page owns each detail; related pages link there. Existing canonical examples:
fingerprints in conventions-spec, idle predicates in idle-policy, OIDC errors in
api-protocol, and timing records in reference/config-locations#step-timing.
When merging same-audience overlap, keep the better-established URL and repoint
all incoming links/anchors.

### Mechanically checked claims

The MCP tool index, retention chart controls, and gate-run infrastructure
signatures have targeted source-to-spec checks in their owning test suites.
A green build does not prove other prose is true or all companion pages match.
Add checks beside the source they read for specific high-risk comparable facts;
do not build a generic prose checker. See the integration guide's Doc-drift gate.

## Page maintenance

- Root restricts new documentation files to explicit requests. Prefer existing
  pages; never introduce another README for this module.
- Add/move sidebar entries by audience, foundational explanation first.
  When a new page is authorized, register its file ID and verify both rendering
  and navigation.
- Use explicit anchors for headings with fragile generated slugs. Search incoming
  anchors before moving content and again afterward: broken-anchor warnings can
  otherwise survive a successful build.
- Keep build link failures fatal; inspect warnings as well as the exit code.
- Versioning is currently off. Enable deliberate Docusaurus version snapshots
  when adopting GA documentation versioning, not as incidental cleanup.

## Diagram conventions

### Visual vocabulary

| Element | Style |
|---|---|
| External actor/source/sink/terminal endpoint | charcoal #0f1320 (optional gradient to #1a2030), white text, radius 14 |
| Active step/workload | white fill, theme-aware charcoal text, cyan #0891b2 stroke 1.5px, radius 14 |
| Group/namespace boundary | #fbfcfd fill, #cbd5e0 stroke, radius 18 |
| Main flow | solid #0891b2 arrow, 1.5px |
| Intervention/callback | dashed #22d3ee arrow, 1.5px, dash 5 5 |
| Edge label | theme-background pill covering the line, cyan text 11–12px/weight 500 |

### SVG vs Mermaid

Use hand-coded SVG under `static/img` for small concept/hero diagrams needing
uniform dimensions; use Mermaid for complex flows/lifecycles where auto-layout
and editability matter. Prefer Mermaid unless its visual result needs SVG control.
Reuse existing stack, convergence, parallel-channel, and nested-container patterns
in `abstraction-stack.svg`, `os-paths.svg`, `epic-story-tasks.svg`, and
`inside-environment.svg` rather than inventing another vocabulary.

### SVG requirements

- Wrap images in `figure.erun-hero-figure` with relationship-describing alt text.
  Include an accessible SVG title/role; use the site's font stack.
- Root SVG uses viewBox, not fixed width/height. Repeated shapes use symbols/use;
  every use specifies dimensions matching its symbol. Allow visible overflow or
  expand the viewBox around strokes so half the stroke is not clipped. A one-off
  shape can be inlined.
- Theme-aware body labels use currentColor; dark endpoint labels stay white.
  Edge-label pills use `var(--ifm-background-color, #ffffff)`.
- Emit valid XML: escape bare ampersands; use only the five predefined XML entities,
  literal Unicode, or numeric references. HTML entities are invalid. Double hyphens
  are invalid inside XML comments. Run xmllint on every edited SVG.

### Mermaid requirements

Keep shared theme variables in `docusaurus.config.ts`. Define endpoint, step,
and namespace classes at the bottom of each diagram with the shared palette.
Use rounded rectangles, not short-label stadiums/ellipses. Apply classes through
flow node syntax or explicit state classes, and label state transitions.
Use invisible ordering links only when auto-layout needs them.

### Self-check before shipping a diagram

Build and inspect the rendered page before presenting it. Check XML, symbol
dimensions/stroke overflow, all elements inside viewBox, arrows ending at shape
edges, label pills fully covering lines, and every Mermaid class defined.
Compare color, alignment, shape consistency, legibility, and flow with existing
assets at the figure's 920px maximum and a smaller ~640px/mobile viewport.
A parsed file alone is not visual verification.

Do not publish architecture screenshots containing private infrastructure,
tokens, or environment-specific secret URLs; use abstract diagrams and safe examples.
