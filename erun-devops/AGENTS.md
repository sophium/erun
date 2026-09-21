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
  chart default, and clear/recompute the fingerprint. Bumping `VERSION` alone
  builds an image nothing deploys; `image-version_test.sh` fails the render gate
  when a chart default and its `docker/<image>/VERSION` disagree, so the bump and
  the default move together or neither lands.
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
- Agent MCP configuration is reconciled once per container boot, never per shell.
  The shell hook is sourced by every interactive shell and every `sh -lc` remote
  exec, so run each configure script under one container-lifetime claim
  (`/tmp/erun-agent-config`, tests via `ERUN_AGENT_CONFIG_STATE_DIR`), never the
  home PVC. `entrypoint_test.sh` locks exactly-once structurally, not by wall clock.
- The IMDS region probe pays one timeout, not two. Where nothing answers the
  link-local address the probe drains curl's whole budget; skip the IMDSv1 fallback
  on that timeout and bound the connect phase. Keep both halves.

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
- Size the runtime container's own default for the gate it runs, not for a serving
  app: agents run `make check-gate` in it — the same ten-target gate the sidecar
  runs during an image build. `DefaultRuntimePodMemory` and the chart's
  `runtime.resources.limits.memory` fallback move together, and a lowered limit
  re-creates the OOM kills that destroy an in-pod agent run and its unpushed work.
  A cold full gate has been measured pinning a 6Gi limit (peak equal to the limit,
  ceiling hits throughout lint) against 78% of 16384Mi with no ceiling hits, and
  a warm one at about a quarter of it.
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
  Preserve canonical OIDC origin. Nginx's SPA fallback must not return HTML for a
  missing static asset: carve out any request whose final path segment carries a
  file extension, not just the `/assets/` prefix, so a root-level file
  (`favicon.svg`) or a probed `/favicon.ico` 404s instead of serving the shell.
  The same fallback must not answer a conventional probe path the console does
  not implement — a monitor's predicate is "2xx", so a shell served at `/health`
  reports healthy unconditionally, the fail-open shape this carve-out exists to
  remove; such paths 404 and name the endpoints that do answer (`/healthz`,
  `/version.json`). Keep that set named rather than "any dotless path that is
  not an app route": an unknown app route must keep serving the shell. The
  `/v1/` proxy location takes `^~` so it stays ahead of both regex locations.

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
- **Incremental promotion never skips a Dockerfile matching the test-stage-gate
  convention (`AS test` plus a later `COPY --from=test`), however unchanged its
  inputs are.** A matching fp-tagged image proves the *inputs* are unchanged, not
  that the gate ran: promoting one reports the same exit 0 as a build that actually
  ran `make check`. Detect the convention by Dockerfile content
  (`dockerfileHasGateTestStage`), always rebuild such a Dockerfile instead of
  promoting it, and refuse outright if a build is ever marked both `GateTestStage`
  and `Promote`. This is deliberately narrower than disabling the Docker build
  cache generally: BuildKit's per-instruction layer cache inside a real
  `docker build` is untouched. `build_gate_test_stage_test.go` locks the detection
  and the refusal.
- The test stage is also the definition other in-container runs of the Playwright
  suite mirror, so its environment carries `ERUN_PLAYWRIGHT_ARTIFACTS_DIR`: the
  suite's one artifact root (Playwright's output dir, the HTML report, every frame a
  spec captures) pointed at a container-local path. A run that mirrors the stage by
  bind-mounting a worktree over `/src` as root — `scripts/repro-gate-contention.sh`
  does — otherwise leaves artifacts owned by uid 0 in a tree the environment user
  owns, where `rm -rf` cannot remove them and every later run in that environment
  fails with a bare `EACCES` inside whichever spec writes first, for every branch.
  Change the value here and the mirror changes with it.
- Previews show concrete commands for the operations selected, without adding
  build/push actions to a pure deploy.
- **A test needing a real container runtime reaches it from a `RUN` step via the
  BuildKit `network.host` entitlement, which `erun build` grants.** Plain
  `docker build` refuses `RUN --network=host` with `network.host is not allowed`;
  `docker build --allow network.host` lifts it, with no separate container-driver
  builder instance needed. Verified live in this repo's own `remote-agent` pod: with
  the flag, a `RUN --network=host` step reached the pod's own dind sidecar at
  `DOCKER_HOST=tcp://127.0.0.1:2375` and ran a real container end to end.
  `dockerBuildEntitlementArgs` passes the flag on a build whose Dockerfile declares a
  `test` stage — the only build that has anywhere to run tests — so a component test
  stage can depend on it. It is deliberately not passed to a Dockerfile with no
  `test` stage: the entitlement hands a build step the *builder's* network namespace,
  which is this environment pod's, and a production image has no use for it. Such a
  build keeps BuildKit's default deny and fails loudly at LLB load if it asks anyway.
  `build_network_entitlement_test.go` locks both arms and that the flag is paired with
  its value; `build/dry_run_dockerfile_test_stage_grants_host_network_entitlement`
  locks the granted arm end to end. The grant is a consequence of erun owning the
  builder, not a safe default, and it must be documented as such: a `RUN
  --network=host` step can reach the daemon that is building it, and start, stop,
  prune or inspect the containers and images of its own build. A component's test
  stage is trusted code running against its own environment's runtime, not a sandbox.
- **The TCP endpoint that makes the above reachable is not deliberately wired up.**
  It exists because the dind sidecar always runs with `DOCKER_TLS_CERTDIR=""`, and
  the vendored `docker:*-dind` image then adds an insecure
  `--host=tcp://0.0.0.0:2375` listener bound to *all* interfaces, with no
  authentication, reachable by anything sharing the pod's network namespace. That is
  a real pre-existing exposure this repo has not hardened to loopback-only.
- **Under that entitlement a test may start its own container-runtime fixture; two
  classes never belong in a `test` stage.** In scope: a Testcontainers-style
  ephemeral dependency (a postgres, a compose-style sidecar) via
  `RUN --network=host` + `DOCKER_HOST=tcp://127.0.0.1:2375` — it needs a daemon, not
  a deployment, and the build already has one. Out of scope permanently: a test
  needing the build's own output (`erun-ui/playwright` needs a built `erun-app`, and
  this stage cannot depend on the `builder` stage it gates without inverting the
  marker order), and a test asserting a deployed version (that runs after `deploy`,
  per the `/pipeline` convention, never during build). The concrete in-scope
  instances are `erun-backend-db/migrate_test.sh`, `retention*_test.sh`,
  `schema_drift_test.sh`, and `erun-console/nginx_test.sh` — each needs only a real
  docker daemon (`migrate_test.sh` additionally needs the atlas CLI, a toolchain
  `COPY` away) — and none is migrated into a component `test` stage yet: they remain
  the root Makefile's `test-postgres-restart`/`test-retention`/
  `test-retention-grants`/`test-schema-drift`/`test-console-nginx` targets, run by
  hand or via `erun exec job` before merging a change to the behavior they cover.
  Retiring them is now blocked only by the migration itself: the entitlement above
  has landed, so nothing but the per-component `test` stage work remains. Until that
  lands they stay runnable only by hand or via `erun exec job`, never in `make check`
  — see the venue note in the root Makefile, which is a real constraint rather than
  an oversight.

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
