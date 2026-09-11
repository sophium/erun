# AGENTS.md

Additional guidance for `erun-devops`. Follow root guidance and the shared Go
conventions in `erun-common/AGENTS.md` for Go changes here.

## Scope

Own runtime/base images, Kubernetes charts, Terraform modules, and Linux packaging.
Keep shared runtime assets and the tenant-generation contract aligned. Command
composition and release invariants belong to root/shared logic, not chart policy.

## Runtime Image Rules

- Preserve the base-image dependency graph and publish bases before dependents.
  Put genuinely shared OS setup in `erun-ubuntu`; keep runtime-specific wiring here.
- Pin tooling and downloads. `GOLANGCI_LINT_VERSION` at repository root is the
  shared pin for image, local lint, and hook checks; do not add independent defaults.
  Put stable expensive layers before source copies; architecture-specific layers
  are separate cache boundaries.
- Published base fingerprints live in `.erun/config.yaml` under
  `docker.fingerprints`. Derive, never guess, the fingerprint; update it with the
  published content. A changed base requires a new tag because `IfNotPresent`
  may retain the old digest. For dind, advance the trailing revision, update the
  chart default, and clear/recompute the fingerprint.
- Keep `ERUN_OUTPUTS_DIR` creation/export aligned in Dockerfile, chart, and
  entrypoint. Deliverables persist on the home PVC, separate from git work and
  paste attachments.
- Skills come from `erun-skills/skills`, copied as whole trees. Both assistant
  config initializers use the single `skills-install.sh` helper: install absent,
  refresh unchanged/legacy copies, preserve operator edits using the baked-hash
  marker. Test all four cases; source and marketplace rules live in
  `erun-skills/AGENTS.md`.
- Prune stale dtach sockets/owners only at container boot, never on shell entry.
  Keep sockets in the container-lifetime directory, not the home PVC, and keep
  its path distinct from the desktop process name. Preserve live sockets and
  validate pruning plus boot/shell routing with their script tests.
- Worktree adoption runs only from the chart init container, while both volumes
  are visible off the live home path. Copy a real legacy repo only into an empty
  claim; prove the copy before setting aside the original. Ignore the partial-copy
  marker when deciding whether retry is safe. Never relocate adoption to entrypoint.
- Run one activity-monitor loop in every pod. Sample resident CPU activity before
  sleeping; gate only auto-stop on cloud environment type, not sampling.
- Terraform modules are published, version-pinned source references, not baked
  copies. Keep modules provider-free; callers supply provider configuration.
  OCI module distribution remains a proposal, not the current contract.
- Bake tenant artifacts under `/opt/erun/release`, never under the home PVC mount.
  For sourceless runtime environments, `release-link.sh` exposes this tree via the
  repo-path symlink. Preserve populated worktrees, agent environments, and
  mount-source clones. Keep mutable Terraform state, plans, and provider data on
  the home PVC, outside the read-only baked tree. Test adoption/linking preservation
  and interrupted-run recovery.

## Runtime Chart Rules

### Identity, credentials, and RBAC

- Runtime resource names derive from `.Release.Name`; component resource names,
  selectors, and cross-references derive from `.Values.tenant | default "erun"`.
  Keep image keys and code-addressed container names stable across tenants.
- Every dedicated ServiceAccount consumes `.Values.imagePullSecrets`, including
  accounts used by Go-created Jobs. A named account does not inherit the namespace
  default account's pull credentials. Test rendered accounts with configured secrets.
- Change Roles alongside the actual Kubernetes calls that need them. Prefer
  `resourceNames` for known objects; explain collection/create exceptions.
  Account for indirect reads: Helm rollout waiting follows Deployments through
  ReplicaSets to Pods. Test chart grants and, for changed live permissions, execute
  the real operation as that identity; a nominal `auth can-i` check alone is not proof.
- Runtime access is namespaced by default. Keep local contribution grants and
  `platformAccount` cluster-admin opt-ins separate; removing the opt-in must remove
  its binding. Initial privilege escalation requires an already-authorized control
  plane context, not in-pod self-granting. Separate optional RBAC YAML documents.
- Mounted registry credentials need no pod Secret-read permission: kubelet supplies
  the volume. Apply secret payloads on stdin, never argv. At boot merge only missing
  registry entries so newer in-pod logins win; init rotates the configured secret,
  plain deploy carries its reference forward. Preserve the optional/absent mount
  behavior and test it.

### Storage and process lifecycle

- Persist home and Docker state on their own PVCs; moving either or build caches
  to `emptyDir` requires an explicit persistence decision and tests.
- Never set pod `fsGroup`: it recursively changes Docker layer permissions;
  `OnRootMismatch` does not fix dockerd resetting its root ownership.
  `prepare-volumes` chowns only home/worktree PVC mount points before adoption,
  never contents, host worktrees, Docker storage, or the socket volume.
- For PVC worktrees, stage legacy home and new claim outside `/home/erun`.
  If adoption is needed but the image lacks its helper, refuse startup rather than
  masking the repo with an empty mount. Keep deploy's relocation preview aligned.
- MCP runs supervised inside the runtime container so it uses the tenant toolchain,
  not a stock sidecar. Share argument construction with standalone MCP startup;
  preserve enable/disable compatibility and restart logging.
- Only build-capable environment types get dind, its state PVC/socket, binfmt,
  Docker environment variables, and supplemental group. Runtime environments
  consume published versions and render none of these resources.
- Prefer the daemon's `0660 root:docker` socket via the configured supplemental
  group. Keep any compatibility chmod non-fatal, and bound postStart waiting on
  a responding daemon, not mere socket existence.

### Resource and network boundaries

- Declare dind requests/limits explicitly, not through ambient LimitRange defaults.
  Under embedded BuildKit, build steps can escape the sidecar's memory cgroup:
  its configured memory remains capacity guidance, not a proven aggregate ceiling.
- Do not write limits to a node-shared `docker/buildkit` cgroup. Standalone
  buildkitd configuration is not read by dockerd's embedded builder; daemon-wide
  cgroup-parent changes broke exec/readiness. Per-leaf caps do not prove aggregate
  isolation. Keep this memory-enforcement gap explicit.
- Thread resolved environment CPU/memory limits through
  `applyDindResourceBuildArgs` into the test-stage parallel-gate overrides;
  unbounded cgroup readings must not size fan-out for the entire host.
  Report killed/resource-exhausted builds as resource failures, not lint verdicts.
- CPU enforcement uses a distinct, tested mechanism: `dind-entrypoint.sh` mirrors
  its live `cpu.max` into a per-pod capped parent and in-pod builds pass that parent
  per invocation. Do not apply this to host builds or change daemon placement.
  Preserve unlimited/unreadable fallbacks and test actual flag selection.
- Derive Docker bridge MTU from the pod's default-route interface. Do not hardcode
  it or add an operator-maintained chart value. Keep resolution failure non-fatal.
  Diagnose TLS stalls with comparable pod/container network paths, not architecture
  alone; test route selection, argument order, jumbo MTUs, and fallback behavior.

### Component-specific invariants

- Deploy component dependency order comes from the environment's deployment plan,
  with shared fallback ranking; preserve parallel steps only for independent
  components. Default deployment is runtime-only; components remain opt-in.
- PostgreSQL reset is a snapshot-only post-install/post-upgrade network hook, never
  part of the Deployment pod lifecycle or an on-disk PGDATA wipe. Pod restarts must
  preserve data. The migration release has its own hook plus a periodic repair
  CronJob using the same idempotent migration command. Retention controls and
  policies belong to the DB guide; test rendered controls and their public docs.
- API consumes the PostgreSQL secret and follows migrations. Registry bearer auth
  is delegated to the API token service: require token realm and an existing public
  signing-key Secret; expose the destructive retention window as an explicit value.
- Zitadel needs both core and Login V2, shared bootstrap PAT handoff, both ingress
  paths, and a consistent external origin. Require an operator-supplied masterkey
  Secret, never generate a default. Sequence after its database.
- Docs publishing is an explicitly enabled Job, not a Service/Ingress. Credential
  and hosting setup are owned by `erun-docs/AGENTS.md`. Stamp deployed docs and
  console `version.json` from the image build version, not checked-in static files.
- Console apex/www redirects target the configured canonical console origin.
  Enable with either a base domain or explicit apex, support explicit disable, and
  record the resolved state/reason in the status ConfigMap. Share certificate SANs
  and Secret without racing a second issuer request; DNS is separate configuration.
  Preserve canonical OIDC origin. Nginx's SPA fallback must not return HTML for
  missing hashed assets.

## Wrapping And Pinning Third-Party Service Images

- Use `docker/<name>/VERSION` as the upstream pin and a passthrough Dockerfile
  (plus genuinely required additions). Publish the wrapper under our registry
  namespace; charts use `imageOverrides` with defaults matching the pin.
- Register and sequence the opt-in component through shared deployment contracts.
  Extra networking, storage, or config extends this pattern, not a special image path.
- Record the derived published fingerprint on first release; clear/recompute it
  when wrapper content or its pin changes. Version-pinned wrapper fingerprints
  deliberately exclude the release-versioned chart: chart-only changes must not
  churn an unchanged upstream image.
- Override inherited OCI source labels to this repository. Provenance and registry
  visibility are separate; a source label or public repository does not make a
  private package anonymously pullable. Verify access independently.

## Build Workflow

- Follow root's pure primitives: build mints artifacts/version, push publishes,
  deploy installs an explicit published version. Build defaults to both supported
  architectures; non-release builds may explicitly select a platform. Releases
  require the full platform set. Do not describe deploy as a build or push path.
- Keep `make check` in the image test stage and its success-marker dependency
  before the builder's heavy compilation steps, preventing independent stages
  from competing with the gate.
- Pin native tests, cross-compilers, and architecture-independent builders to
  `BUILDPLATFORM`. Do not pin final target layers or stages executing target
  binaries. Copy target toolchains from an unpinned, copy-only stage when needed.
- The test stage must carry the inputs and pinned toolchains for every wired gate:
  all Go test/lint modules, all Yarn workspace members, generated Wails bindings,
  Helm chart tests, Wails/webkit dependencies, and Playwright Chromium dependencies.
  Keep the root Makefile and Docker COPY set aligned as members/gates change.
  Windows cross-compilation needs no Windows SDK here; it does not prove native UI.
- Use sequential per-platform plain `docker build`, local arch tags, and fingerprint
  tags. Push assembles the per-arch images into a manifest list; preserve
  `--provenance=false`. Validate required binfmt support before building.
- Keep BuildKit and its cache mounts. The buildx plugin may provide plain Docker's
  frontend, but do not replace this workflow with a separate builder or push-only
  multiarch invocation: promotion depends on results remaining in the local store.
- Fingerprints include relevant source/COPY inputs, ignores, component charts except
  pinned wrappers, and consumed `ERUN_VERSION`. A new version embedded in an image
  is a different artifact even without source changes. Snapshot identity uses its
  stable base-snapshot value. Promotion requires every requested architecture;
  do not silently reuse an incomplete platform set.
- Previews show concrete commands for the operations selected, without adding
  build/push actions to a pure deploy.

## Release Workflow

- Root/shared release orchestration owns ordering: local stamp/commit/tag, build
  and publish all resolved artifacts, registry read-back, then public tag,
  packaging-checksum sync, version bump and branch publication. A failed publish
  must leave the releasing version retryable and no public tag.
- Keep chart version/appVersion and installer version/checksums coherent.
  Candidates use the same orchestration.
- Tenants consume published shared charts, not copied forks. Default runtime
  customization is an image override. Pod-shape changes use a thin dependent
  umbrella published with the tenant image; components may use similar umbrellas.
- Preserve published tenant-umbrella preference and fallback to the shared runtime
  chart. A tenant umbrella defaults to its matching tenant image unless explicitly
  overridden; an image-only deployment still needs its runtime-image configuration.
  Publish shared changes before deploying consumers.

## Testing Expectations

- Test Docker dependency/cache/version contracts and shared plus tenant chart
  behavior. Keep release-sensitive common, CLI, and MCP suites aligned.
- `helm-chart-tests` uses render-only tests without a cluster; new tests under
  `k8s` are discovered automatically. Keep real-daemon tests separate and explicit:
  `test-postgres-restart` for reset/migration/recovery and `test-console-nginx`
  for shipped nginx behavior. Run affected entrypoint/helper script tests too.
- Live RBAC, resource isolation, and restart guarantees need corresponding real
  probes when changed; render tests alone cannot establish them.
- Guidance-only edits use root's consistency/reference validation exemption.
