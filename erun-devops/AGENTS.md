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
- Pin tooling and downloads. A Go module pins golangci-lint as a `tool` directive
  in its own `go.mod` and runs it as `go tool golangci-lint run`, unless the tool's
  dependency graph would move the module's shipped dependencies (dns01-webhook:
  cert-manager/k8s.io) — then its test stage pins it as
  `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@<version>`. The
  runtime image ships no lint binary. Put stable expensive layers before source copies;
  architecture-specific layers are separate cache boundaries.
- Published base fingerprints live in `.erun/config.yaml` under
  `docker.fingerprints`. Derive, never guess, the fingerprint; update it with the
  published content. A changed base requires a new tag because `IfNotPresent`
  may retain the old digest. For dind, advance the trailing revision, update the
  chart default, and clear/recompute the fingerprint. Bumping `VERSION` alone
  builds an image nothing deploys; `TestChartImageTagsMatchTheirVersionPins`
  (erun-common) fails the build when a chart template's image tag and its
  `docker/<image>/VERSION` disagree, so the bump and the default move together
  or neither lands.
- The runtime image's entrypoint is `erun runtime-init`
  (erun-common/runtime_entrypoint*.go), and it is the container's init: it reaps
  orphaned zombies (the daemonised sshd's per-connection children), forwards
  termination signals to what it started and exits with that work's code, or
  128 + the signal. Keep those duties in Go and locked by
  `runtime_supervisor_linux_test.go`; the image runs no tini and no shell
  entrypoint. Every boot step is best-effort and logged, so a partly configured
  pod stays reachable. No product logic lives in shell here: `bash_prompt.sh` is
  a sourced prompt snippet only.
- Keep `ERUN_OUTPUTS_DIR` creation/export aligned in Dockerfile, chart, and
  entrypoint. Deliverables persist on the home PVC, separate from git work and
  paste attachments.
- Skills come from `erun-skills/skills`, copied as whole trees. The boot, `erun
  install-skills` and the desktop orchestrator share one install-or-refresh
  (`InstallOrRefreshSkills`/`InstallOrRefreshAgents`): install absent, refresh
  unchanged/legacy copies, preserve operator edits using the baked-hash marker.
  `agent_assets_install_test.go` tests all four cases; source and marketplace
  rules live in `erun-skills/AGENTS.md`.
- Keep dtach sockets in the container-lifetime directory (`/tmp/erun-sessions`),
  not the home PVC, so a container starts with none, and keep its path distinct
  from the desktop process name.
- The runtime chart's init containers and dind sidecar run the runtime image's
  own erun (`adopt-worktree`, `dind-entrypoint`), so the image must be at least
  as new as the chart. `erun deploy` refuses a stock `erun-devops` image older
  than the stock chart (`guardRuntimeImageNotOlderThanChart`); a tenant image's
  erun base is re-pinned with the chart (`erun-build-env`, `erun-pin-version`).
- Worktree adoption (`erun adopt-worktree`, erun-common/worktree_adopt.go) runs
  only from the chart init container, while both volumes are visible off the live
  home path. Copy a real legacy repo only into an empty claim; prove the copy before
  setting aside the original. Ignore the partial-copy marker when deciding whether
  retry is safe. Never relocate adoption to entrypoint.
- Run one activity-monitor loop in every pod. Sample resident CPU activity before
  sleeping; gate only auto-stop on cloud environment type, not sampling.
- Terraform modules are published, version-pinned source references, not baked
  copies. Keep modules provider-free; callers supply provider configuration.
  OCI module distribution remains a proposal, not the current contract.
- Bake tenant artifacts under `/opt/erun/release`, never under the home PVC mount.
  For sourceless runtime environments, the boot (`LinkRuntimeRelease`) exposes
  this tree via the repo-path symlink. Preserve populated worktrees, agent environments, and
  mount-source clones. Keep mutable Terraform state, plans, and provider data on
  the home PVC, outside the read-only baked tree. Test adoption/linking preservation
  and interrupted-run recovery.
- Agent configuration (instructions, skills, Claude settings relay and hooks,
  Codex config, MCP wiring) is reconciled once per container boot by the
  entrypoint, never per shell: the shell hook is sourced by every interactive
  shell and every `sh -lc` remote exec, so it only records CLI activity. The
  Claude settings relay removes only what its `settings.json.erun-relayed.json`
  record says it wrote.
- The boot writes the erun config from `ResolveInjectedRuntimeConfig`, the same
  projection `erun doctor --sync-config` reconciles against, so a booted pod is
  in sync by construction. Region and instance id come from the chart's
  `ERUN_CLOUD_REGION`/`ERUN_CLOUD_INSTANCE_ID`; a deploy from a host without
  the environment's cloud context renders them empty, so on AWS the boot fills
  an empty one from IMDS once (`runtime_cloud_metadata.go`: v2 token, 2s
  timeout, no v1 retry after a timeout).
- Idle stop asks the real AI clients to quit before the host powers off: match
  them by executable path inside their npm packages (`@anthropic-ai/claude-code`,
  `@openai/codex`) or by program name, never by a wrapper name the image no
  longer has (`isRuntimeAIClient`).
- `/usr/local/bin/claude` is a symlink to `erun`, made after the npm install
  so the install cannot replace it; erun dispatches on that argv[0] and execs
  the real binary in the npm package (`ERUN_CLAUDE_REAL` overrides it), adding
  `--continue` when the directory has a conversation and no argument picks a
  mode or subcommand of its own (`erun-common/claude_resume.go`,
  `claude_resume_test.go`; `devops_dockerfile_contract_test.go`).

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
  An image whose erun lacks `adopt-worktree` fails the init container and so
  refuses startup rather than masking the repo with an empty mount. Keep deploy's
  relocation preview aligned.
- The erun-dind sidecar runs `erun dind-entrypoint` from the `erun-bin` emptyDir,
  which `prepare-volumes` fills with the runtime image's own `erun`; the stock dind
  image carries none, and the shared binary keeps the sidecar on the pod's version.
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
- A bound on the node's build cache travels only when the deployment declares the
  node. `buildCacheNodeGi` and `buildCacheCoTenants` both default to `0` and render
  only as a pair, and only for an environment that has the dind sidecar, so a
  deployment that says nothing about its node emits neither variable and keeps the
  docker-volume ceiling unchanged. Neither is discoverable from inside the pod —
  the environment's RBAC is namespace-scoped, so it cannot count its co-tenants,
  and a node-local, quota-less claim reports the node's capacity rather than its
  own size — and nothing in erun's own deploy plan sets them, so they are an
  operator's declaration alone. What they resolve to, and the co-tenant count at
  which they stop changing any ceiling, is `erun-common/AGENTS.md` § "Release
  recovery".
- Size the runtime container's own default for the builds and tests it runs, not
  for a serving app. `DefaultRuntimePodMemory` and the chart's
  `runtime.resources.limits.memory` fallback move together, and a lowered limit
  re-creates the OOM kills that destroy an in-pod agent run and its unpushed work.
  A cold full repository gate was measured pinning a 6Gi limit (peak equal to the
  limit, ceiling hits throughout lint) against 78% of 16384Mi with no ceiling hits.
- **The sizing warning above runs one way only, and a bigger limit is not the safe
  direction.** A limit is containment for the container holding it, not a claim the
  node can back it. Every environment pod is Burstable at priority 0: requests are
  a small fixed value (the chart's 0.25 CPU / 1 GiB per container) and a limit is
  never set equal to one, so no container satisfies the `requests == limits` rule
  `Guaranteed` requires, and no environment pod carries a `PriorityClass` (the
  repo's only one is the cluster-edge local-path helper). Node-pressure eviction
  sheds pods whose usage exceeds their requests first, ranked by priority — every
  environment is 0 — so raising a limit buys burst headroom and widens what the
  environment can be holding when a node-level killer looks; it buys no safety.
  Container OOM scores do not come from the limit: kubelet derives a Burstable
  container's `oom_score_adj` from its *request* and the node's own capacity
  (`1000 - 1000 × request / nodeCapacity`), so it is identical for every
  environment on a node however each is sized. A container that reaches its own
  `memory.max` is killed on an otherwise idle node regardless. What bounds the sum
  is the namespace `ResourceQuota` (`MinimumRuntimeNamespaceQuota`), not any
  container's limit, and `erun init` sets none. The operator-facing prose is
  `erun-docs/docs/desktop/resources-and-usage.md`; keep the two in step.
- Do not write limits to a node-shared `docker/buildkit` cgroup. Standalone
  buildkitd configuration is not read by dockerd's embedded builder; daemon-wide
  cgroup-parent changes broke exec/readiness. Per-leaf caps do not prove aggregate
  isolation. Keep this memory-enforcement gap explicit.
- BuildKit attributes its cache records and cache mounts to the daemon's own
  engine id (moby's builder passes `ID: opt.EngineID`), which dockerd persists
  at `<data-root>/engine-id` on the docker-state volume and reuses on every
  later start, so the *namespace* those keys live in is a property of the
  volume and survives a roll, `<engine-id>::<ref>` keys and all. What the
  volume holds is not thereby servable: a build's RUN steps are keyed under a
  `--cgroup-parent` derived from the pod's own hostname
  (erun-common/build_cpu_cap.go), so a roll re-executes them while only the
  steps before the first one still replay. Do not anchor a worker id in
  `erun dind-entrypoint`: `<buildkit>/workerid` is read only by standalone
  buildkitd's runc/containerd workers, so writing one changes no key.
  erun-common/dind_entrypoint.go carries the measured detail beside the code.
- Report killed/resource-exhausted builds as resource failures, not lint verdicts.
- Size the dind CPU cap from the node, never as a flat safe-looking constant:
  a CPU limit is a ceiling, not a reservation, so a build capped well under the
  node is throttled while the node sits idle, which is what a CPU-pressure
  figure near 100% beside a load average far below the core count means.
  `RuntimeDindCPULimit` (erun-common/runtime_resources.go) owns the rule — the
  node's CPUs divided across the build-capable environments expected to be
  building on it at once, floored at `MinimumRuntimeDindCPU` — and
  `DefaultRuntimeDindCPU` and the chart's `runtime.dind.resources.limits.cpu`
  fallback are two copies of one number that move together
  (`runtime_dind_default_mirrors_test.go`).
- CPU enforcement uses a distinct, tested mechanism: `erun dind-entrypoint` mirrors
  its live `cpu.max` into a per-pod capped parent and in-pod builds pass that parent
  per invocation. Do not apply this to host builds or change daemon placement.
  Preserve unlimited/unreadable fallbacks and test actual flag selection.
- Derive Docker bridge MTU from the pod's default-route interface. Do not hardcode
  it or add an operator-maintained chart value. Keep resolution failure non-fatal.
  Diagnose TLS stalls with comparable pod/container network paths, not architecture
  alone; test route selection, argument order, jumbo MTUs, and fallback behavior
  (erun-common/dind_entrypoint_test.go). The Linux-only wiring (procfs, sysfs,
  execve) sits behind build tags; elsewhere the command refuses.

### Component-specific invariants

- A node under DiskPressure must still be able to reclaim its own storage.
  kubelet's eviction manager admits and evicts by `IsCriticalPod` — a static or
  mirror pod, or one whose resolved `spec.priority` reaches
  `scheduling.SystemCriticalPriority` — and the local-path provisioner pins its
  helper pod onto the volume's own node by setting `spec.nodeName` itself, so
  the scheduler never sees it and a helper pod with no `priorityClassName` is
  refused while the node has conditions and evicted when it has any. Both
  provisioning and reclamation run that pod, so neither a new PVC nor the
  deletion of an existing one completes on the node that needs the space. A
  toleration is not the fix — the provisioner appends the disk-pressure one
  itself. The cluster-edge module's `install_local_path_helper_pod_resilience`
  is the opt-in that merges the priority class in; it must also annotate the
  provisioner Deployment, because k3s sets no `CONFIG_MOUNT_PATH` and the
  provisioner's own 30s reload is therefore a no-op.
  `terraform-erun-cluster-edge/tests/local_path_helper_pod.tftest.hcl` locks it.
- Deploy component dependency order comes from the environment's deployment plan,
  with shared fallback ranking; preserve parallel steps only for independent
  components. Default deployment is runtime-only; components remain opt-in.
- PostgreSQL reset is a snapshot-only post-install/post-upgrade network hook, never
  part of the Deployment pod lifecycle or an on-disk PGDATA wipe. Pod restarts must
  preserve data. The migration release has its own hook plus a periodic repair
  CronJob using the same idempotent migration command. Retention controls and
  policies belong to the DB guide; test rendered controls and their public docs.
- Every CronJob chart sets `ttlSecondsAfterFinished` alongside
  `failedJobsHistoryLimit`. The history limit bounds how many failures are kept
  and never expires them on age, so a healthy database reported weeks-old `Error`
  pods forever — noise a real regression hides behind. Size the TTL from the
  schedule: long enough that a live break always has a fresh failure to read, and
  that a failing daily sweep outlives the 24h to its next attempt, but short
  enough that a stale failure stops reading as current state. No render test
  sweeps for it, so review every CronJob template for the pair.
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
- Every component tests itself as part of its own build, in the stage chain
  `erun-docs/docs/agent-reference/dockerfile-stages.md` specifies: `build`
  (toolchain, deps, source, native compile; no version stamp, no `TARGET*`
  args) -> `test` (`FROM build`; only the component's own plain commands: `go
  tool golangci-lint run`, `go test ./...`, `yarn test`) ->
  optional helpers `FROM test` (`dist`: version stamp and target-arch compile)
  -> `runtime`, the last stage. `runtime` copies what it ships from `test` or a
  helper built `FROM test`, never from `build`, so BuildKit cannot prune `test`
  and no image exists when it fails. There is no marker file. Download-only
  stages that read no other stage (a target-arch tool fetch) may feed
  `runtime`, and context files may be copied straight in. A part with nothing
  to compile has a standalone test stage that COPYs what it tests. Test logic
  belongs to the component, never to this module.
- A Dockerfile with several parts prefixes each part's stages
  (`<part>-build` -> `<part>-test`) and joins every `<part>-test` in the one
  stage named `test`, which is what erun detects. The `erun-devops` image
  keeps that shape with one part: `cli-build` -> `cli-test` lints and tests
  `erun-common`, `erun-mcp`, `erun-cli` and `erun-integration`, and builds
  `erun-cli` and `erun-mcp` for darwin and windows so the Homebrew and Scoop
  source builds are proven to compile
  (`devops_dockerfile_contract_test.go`); `test` is FROM
  `cli-test`; `dist` (FROM test) stamps `ERUN_VERSION` into `erun` and `emcp`
  for the target arch; `runtime` takes them from `dist`. There is no
  `erun-cli` image and no scripts stage: the image bakes no product scripts,
  and the Terraform modules' `terraform test` suites have no build runner
  (run them by hand with `terraform test` in the module). `cli-test` copies
  only the Dockerfile from `docker/erun-devops/` (what the Go suites read),
  every other `docker/<image>/` directory whole and `linux/` whole;
  `TestCLITestStageCopiesEveryDockerDirectory` fails when a new image
  directory is missing from that list.
- `linux/<name>/Dockerfile` is a package component: it builds a `.deb`, never
  an image. Same chain, ending in `dist`, which is `FROM scratch` and holds
  only the `.deb` copied from a packing helper (`package`), so
  `--target dist --output type=local,dest=<dir>` exports the package alone.
  `linux/erun-ui` (package `erun-desktop`: `erun-app`, its `.desktop` entry
  and icon) generates the Wails bindings and builds the bundle and a native
  `erun-app` in `build`, and runs the frontend checks and unit tests, the Go
  lint, `go test -race`, the Windows cross-compile, the Playwright package's
  checks and the full Playwright suite in `test` — plain commands, no module
  script; `package` (FROM test) compiles erun-app for
  `TARGETARCH` (a foreign arch through Debian multiarch: crossbuild-essential
  plus that arch's gtk/webkit `-dev` packages) and takes `Depends` from
  `dpkg-shlibdeps`, so the package names its libraries as the build image's
  Debian release does (Debian 13 / Ubuntu 24.04 and later). A `package` stage
  runs only plain `go -C /src/erun-common run ./cmd/debpkg` commands with
  flags: `cross-deps` installs a foreign target's toolchain and `-dev`
  libraries (nothing for a native one), and `build` compiles the Go binaries,
  copies `--file`s, writes `DEBIAN/control` (Depends from `--shlibdeps`) and
  runs `dpkg-deb`; packaging logic goes there, tested, never into shell in the
  Dockerfile. `linux/erun-cli`
  (package `erun`: `erun`, `emcp`, `eapi`, the control fields and copyright
  the former `build-deb.sh` produced) has no test stage: its code is tested
  in this image's `cli-test` and in `erun-backend-api`. A plain `erun build`
  runs a package through `test` only; `erun build --release` builds `dist`
  per architecture and uploads the `.deb`s to the GitHub release.
- `erun-common/dockerfile_copy_contract_test.go` pins this shape for the named
  component Dockerfiles (`componentDockerfiles`) and package Dockerfiles
  (`linuxPackageDockerfiles`), and requires every other
  Dockerfile here to stay a single-stage wrapper of an upstream image (the
  postgres, dind, oci-registry, powerdns, zitadel, zitadel-login and
  erun-ubuntu images), whose last stage is its runtime by position; renaming
  their stages would change pinned fingerprints.
- Pin native tests, cross-compilers, and architecture-independent builders to
  `BUILDPLATFORM`. Do not pin final target layers or stages executing target
  binaries. Copy target toolchains from an unpinned, copy-only stage when needed.
- A test stage COPYs its component plus exactly what its tests read and installs the
  pinned toolchain they run with; a path its tests open that the stage does not
  COPY fails in the build while a full checkout passes.
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
- **An unchanged component's fingerprint promotion is its last successful
  build, tests included, and is a valid result.** A Dockerfile with a consumed
  test stage (`AS test` plus a later `FROM test` or `COPY --from=test`)
  promotes from its fp tag
  like any other image, because fp tags are written only after a successful
  build, and a BuildKit cache hit on the test stage reuses a tested layer the same
  way. `build_test_stage_test.go` locks promotion and rebuild for such a
  Dockerfile.
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
- **`erun build` provides that endpoint where nothing listens on it.** A build
  with a `test` stage first checks `127.0.0.1:2375`; inside a pod the dind listener
  answers and is used. Anywhere else (a host environment, a laptop) it starts a
  loopback-only forwarder from that address to the build's own daemon socket on the
  daemon host's network — the network a `RUN --network=host` step joins — and
  removes it when the build ends (`ensureBuildDaemonEndpoint`). A test stage can
  therefore start container fixtures the same way on every venue.
- **Under that entitlement a test may start its own container-runtime fixture; two
  classes never belong in a `test` stage.** In scope: a Testcontainers-style
  ephemeral dependency (a postgres, a compose-style sidecar) via
  `RUN --network=host` + `DOCKER_HOST=tcp://127.0.0.1:2375` — it needs a daemon, not
  a deployment, and the build already has one. A test needing a built binary
  uses the one `build` produced for the build platform; the shipped target-arch
  binary comes later, from a helper built `FROM test`. Out of scope permanently:
  a test asserting a deployed version or driving a deployment — that is an e2e
  suite (below), run after `deploy`, never during build.
- **`erun-backend-api` is the template for that venue.** Its test stage runs
  `go test ./...` in a `RUN --network=host` step against
  `DOCKER_HOST=tcp://127.0.0.1:2375`; its database suites start PostgreSQL
  themselves through Testcontainers, so nothing in this module decides which
  suites run. The retention and schema drift checks are among those Go suites.
- **Deployed-environment suites live in `e2e/<service>/`**, one Dockerfile each
  with a `deps` stage and an exact `AS e2e` stage, the directory named after the
  exposed service it targets (`e2e/console/`, see its `AGENTS.md`). Only `erun
  e2e` builds them (`--target e2e --no-cache-filter e2e --output
  type=cacheonly`), against the exposed HTTPS URL with the certificate
  verified; `erun build` and `push` refuse them and discovery never walks
  `e2e/`. They are never part of a build, release, or the merge gate. A suite
  receives its URLs and deployed version as `ERUN_E2E_*` build args and its
  credentials as `e2e.secrets` BuildKit secrets; it must not set
  `ignoreHTTPSErrors` or a literal `baseURL`. A suite that needs only a built
  bundle or a local API is not an e2e suite: it belongs in the component's
  `test` stage or its own local Playwright package.

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
- Charts under `k8s` have no render-test suite. Cross-file contracts a chart
  shares with Go live in erun-common's structural tests
  (`runtime_dind_default_mirrors_test.go`,
  `TestChartImageTagsMatchTheirVersionPins`); render a changed chart with `helm
  template` for both a runtime and a build-capable environment before merging.
  Keep real-daemon tests separate and explicit (see Build Workflow).
- Live RBAC, resource isolation, and restart guarantees need corresponding real
  probes when changed; render tests alone cannot establish them.
- Guidance-only edits use root's consistency/reference validation exemption.
