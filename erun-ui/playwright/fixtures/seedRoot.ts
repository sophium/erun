import { execFileSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

// Windows CreateProcess cannot exec a "#!/bin/sh" file or a .cmd/.bat batch
// file, so the backend (and every erun/shell child it spawns) needs real PE
// stubs on PATH. On win32 the stubs are <name>.exe built from fixtures/winstub;
// on POSIX they stay extensionless shell scripts. See writeStubBinary.
const isWindows = process.platform === 'win32';

// seedRoot owns the suite's isolated config root: the headless backend and
// every `erun` child it spawns run against a throwaway HOME under this root, so
// the suite never touches the developer's real ~/.erun / ~/.config/erun and
// every machine sees the same deterministic baseline. Directory layout mirrors
// erun-integration/internal/env.New and the seeded config tree mirrors
// erun-integration/internal/fixture.SeedTenantEnv — keep both in lockstep when a
// path or config field becomes load-bearing. Two deliberate extensions on top:
// `type: local-agent` (the badge/type contract the sidebar specs assert) and
// `aitool: sh` (the AI tab launches an inert shell instead of a real
// claude/codex process).

// Baseline names every spec can rely on. One tenant, two inert local-agent
// environments. `alpha` sorts (and renders) first; `beta` exists so flows
// that need two envs under one tenant (back-to-back opens) are stageable.
export const SEED_TENANT = 'pw';
export const SEED_ENV_ALPHA = 'alpha';
export const SEED_ENV_BETA = 'beta';
// gamma attaches BOTH an AWS alias and a Cloudflare alias so the per-type env
// cloud-alias selectors are stageable: the General tab must render
// two independent selectors, one per provider type, each pre-selected to the
// env's attachment.
export const SEED_ENV_GAMMA = 'gamma';
// One configured cloud provider alias so the Manage dialog's Cloud alias
// select renders deterministically. The matching `aws` stub keeps its token
// status check instant and offline.
export const SEED_CLOUD_ALIAS = 'pw-aws';
// One configured Cloudflare alias. Cloudflare aliases follow the
// "<token-label>+<account-id>@cloudflare" shape erun-common mints. No token is
// written to the off-config secret store, so its status reads "not_configured"
// deterministically and offline — the alias's scoped-token verify never hits
// the network in the harness. That is the correct staged state for asserting
// the per-type selector, the per-type sidebar row, and the "Verify token"
// (re-verify) action without a live Cloudflare account.
export const SEED_CLOUDFLARE_ALIAS = 'pw-token+0123456789abcdef@cloudflare';
// One persisted, stopped AI orchestrator so the ERUN section's orchestrator row
// is stageable without a running Claude session: its shape-encoded status dot
// and the single "…" that opens the management dialog. Only the persisted
// definition matters for the row — ListOrchestrators reads config, not env
// type — so it links the alpha env and renders stopped on boot.
export const SEED_ORCHESTRATOR = 'pw-orch';

// isolatedRoot resolves the suite-owned root. When run.sh has not exported
// ERUN_PLAYWRIGHT_HOME (a direct `playwright test`), the fresh temp root is
// cached back into the env so every worker shares one root instead of minting
// its own.
export function isolatedRoot(): string {
  const configured = process.env.ERUN_PLAYWRIGHT_HOME?.trim();
  if (configured) {
    return configured;
  }
  const created = fs.mkdtempSync(path.join(os.tmpdir(), 'erun-playwright-home.'));
  process.env.ERUN_PLAYWRIGHT_HOME = created;
  return created;
}

export function isolatedHomeDir(): string {
  return path.join(isolatedRoot(), 'home');
}

function stubsDir(): string {
  return path.join(isolatedRoot(), 'stubs');
}

// stubBinPath is the on-disk path of a stub tool. On Windows the backend
// resolves and execs a real PE (LookPath honours PATHEXT), so stubs carry a
// .exe extension; POSIX stubs are extensionless shell scripts.
function stubBinPath(name: string): string {
  return path.join(stubsDir(), isWindows ? `${name}.exe` : name);
}

// winStubBinary builds the fixtures/winstub multi-call PE once per run and
// caches it inside the stub dir; writeStubBinary copies it per tool name.
let winStubBuilt: string | undefined;
function winStubBinary(): string {
  if (winStubBuilt) {
    return winStubBuilt;
  }
  const out = path.join(stubsDir(), '_winstub.exe');
  const src = path.join(__dirname, 'winstub');
  // GOWORK=off so a stray erun-ui/go.work (workspace mode) cannot shadow this
  // nested module — build strictly against fixtures/winstub/go.mod.
  execFileSync('go', ['build', '-o', out, '.'], {
    cwd: src,
    stdio: 'inherit',
    env: { ...process.env, GOWORK: 'off' },
  });
  winStubBuilt = out;
  return out;
}

function repoDir(): string {
  return path.join(isolatedRoot(), 'repo');
}

function erunConfigDir(): string {
  return path.join(isolatedHomeDir(), '.config', 'erun');
}

// e2eK3dEnabled gates every real-cluster branch below; when false the suite is
// the default inert/offline harness. Enable it only on a Docker + k3d + binfmt
// host — never in the default `run.sh` or `make integration-test`.
export function e2eK3dEnabled(): boolean {
  return process.env.ERUN_E2E_K3D === '1';
}

// kubeconfigPath is the fixed kubeconfig for k3d mode so the backend and specs
// share one isolated kubeconfig and never read the developer's ~/.kube.
// global-setup must write the live cluster's kubeconfig here before the backend
// boots.
export function kubeconfigPath(): string {
  return path.join(isolatedHomeDir(), '.kube', 'config');
}

// backendEnv redirects both erun roots — xdg config home and os.UserHomeDir +
// ".erun" — into the isolated root via HOME + XDG_*, and the PATH-prepended
// stubs keep the backend and every `erun`/shell child it spawns off any real
// cluster, cloud, or docker daemon.
export function backendEnv(): Record<string, string> {
  const home = isolatedHomeDir();
  const base: Record<string, string> = {
    HOME: home,
    XDG_CONFIG_HOME: path.join(home, '.config'),
    XDG_CACHE_HOME: path.join(home, '.cache'),
    XDG_DATA_HOME: path.join(home, '.local', 'share'),
  };
  if (e2eK3dEnabled()) {
    // k3d mode drives a live cluster with the real docker/kubectl/helm and
    // built `erun`; only `aws` stays stubbed (no cloud account). This branch
    // must never run in the default inert mode.
    return {
      ...base,
      PATH: `${stubsDir()}${path.delimiter}${process.env.PATH ?? ''}`,
      KUBECONFIG: kubeconfigPath(),
      // Route erun-common's aws calls to the stub explicitly, independent of
      // PATH order.
      ERUN_AWS_BIN: stubBinPath('aws'),
      ERUN_APP_CLI: process.env.ERUN_E2E_ERUN_BIN ?? 'erun',
    };
  }
  return {
    ...base,
    PATH: `${stubsDir()}${path.delimiter}${process.env.PATH ?? ''}`,
    // Force the desktop's ERun/AI tabs to run the inert `erun` stub. The PATH
    // prepend alone is not enough: resolveCLIExecutable resolves a real
    // erun-cli/bin/erun next to the app binary (a dev build artifact) before
    // falling back to PATH, which makes the env-open specs loop red on a
    // developer machine that has that artifact. This seam pins the stub.
    ERUN_APP_CLI: stubBinPath('erun'),
    // Pin which component charts are "published" per version so the Runtime
    // tab's version-aware Components checklist is deterministic offline (the
    // real path probes the registry). 1.0.0 (the seeded runtime env's version)
    // has all charts; 1.0.90 has a subset; 1.0.50 has none. Names are
    // tenant-prefixed (the pw seed tenant publishes pw-* charts), matching
    // ResolveDeployableComponents. See erun-ui/deploy_components.go
    // chartAvailabilityOverride.
    // erun-devops is listed wherever the runtime chart should exist: the Runtime
    // tab now resolves which chart a version would install, and a version with no
    // chart at all is a blocked deploy (1.0.50, deliberately empty).
    ERUN_CHART_AVAILABILITY_OVERRIDE:
      '1.0.0=erun-devops,pw-backend-postgres,pw-backend-db,pw-backend-api,pw-powerdns,pw-docs;' +
      '1.0.90=erun-devops,pw-backend-postgres,pw-backend-db;1.0.50=;' +
      // ERun-line versions the picker specs deploy: a real ERun version publishes
      // its chart, so the seam has to say so or the Runtime tab would (correctly)
      // refuse to deploy them.
      '1.0.134=erun-devops;1.0.16=erun-devops',
    // The seeded local-agent envs are always inert and never deployed, so no
    // local port they compute should ever be treated as reachable. Unpinned,
    // the port range is a plain host-wide TCP port number outside HOME/XDG
    // isolation: a seeded env can land on the same range a real environment
    // on the same host has genuinely bound (this host's own MCP/SSH
    // forwards), and the occupancy/idle-status checks then read that real
    // environment's live state as the seeded env's own. See erun-ui/app.go
    // withDefaultReachabilityDeps.
    ERUN_LOCAL_PORT_REACHABILITY_OVERRIDE: '0',
    // The Local tab otherwise launches the operator's own $SHELL: a
    // terminal-content spec that selects text by screen position then
    // inherits that shell's dotfile-configured prompt and startup timing,
    // both host-dependent. This pins a real, rc-free POSIX shell with the
    // fixed prompt LOCAL_SHELL_PROMPT below (kept in lockstep with
    // erun-ui/session.go's localShellDeterministicPrompt) so terminal specs
    // can wait for a known prompt instead of guessing which row it lands on.
    ERUN_LOCAL_SHELL_OVERRIDE: '1',
  };
}

// LOCAL_SHELL_PROMPT is erun-ui/session.go's localShellDeterministicPrompt —
// the fixed prompt the Local tab's shell renders once ready, when
// ERUN_LOCAL_SHELL_OVERRIDE is set above. Specs that select terminal text by
// screen position must wait for this to appear first, so the shell's own
// startup output can never race a spec's synthetic printOnlyLine write.
export const LOCAL_SHELL_PROMPT = 'erun-test$ ';

// createIsolatedLayout refuses to run against anything that could be a real
// home directory.
export function createIsolatedLayout(): void {
  const root = isolatedRoot();
  if (!root || root === os.homedir() || root === '/') {
    throw new Error(`refusing to use ${JSON.stringify(root)} as the isolated Playwright root`);
  }
  const home = isolatedHomeDir();
  for (const dir of [
    home,
    path.join(home, '.config'),
    path.join(home, '.cache'),
    path.join(home, '.local', 'share'),
    repoDir(),
    stubsDir(),
  ]) {
    fs.mkdirSync(dir, { recursive: true });
  }
  const stubs = e2eK3dEnabled() ? ['aws'] : ['kubectl', 'helm', 'docker', 'aws', 'erun'];
  for (const name of stubs) {
    writeStubBinary(name);
  }
}

// seedEnvironmentForK3d writes a k3d env with — critically — NO runtimeversion,
// so the desktop's create flow must build → push → deploy a fresh version
// rather than install a pre-pinned one. Its repo is the seeded repo, which
// carries a buildable devops module on the k3d-mode host.
export function seedEnvironmentForK3d(
  tenant: string,
  environment: string,
  context: string,
  registry: string,
): void {
  const envDir = path.join(erunConfigDir(), tenant, environment);
  fs.mkdirSync(envDir, { recursive: true });
  fs.writeFileSync(
    path.join(envDir, 'config.yaml'),
    `name: ${environment}\n` +
      `repopath: ${repoDir()}\n` +
      `kubernetescontext: ${context}\n` +
      `containerregistry: ${registry}\n` +
      'type: local-agent\n' +
      'aitool: sh\n',
  );
}

// seedRuntimeForK3d writes a k3d env that installs the published erun-devops
// runtime BY REFERENCE from ghcr (pinned runtimeversion, no local build): the
// fast create -> deploy -> open path that reproduces the post-init redeploy
// loop (#…) without the multi-minute multi-arch build the builds-here e2e pays.
// The ghcr runtime image + chart are public, so a fresh k3d cluster pulls them
// with no credentials.
export function seedRuntimeForK3d(tenant: string, environment: string, context: string): void {
  const envDir = path.join(erunConfigDir(), tenant, environment);
  fs.mkdirSync(envDir, { recursive: true });
  fs.writeFileSync(
    path.join(envDir, 'config.yaml'),
    `name: ${environment}\n` +
      `repopath: ${repoDir()}\n` +
      `kubernetescontext: ${context}\n` +
      'type: runtime\n' +
      'runtimeversion: 1.0.149\n' +
      'runtimeimage: ghcr.io/sophium/erun-devops\n' +
      'containerregistry: ghcr.io/sophium\n' +
      'aitool: sh\n',
  );
}

// seedRemoteAgentForK3d seeds the env type that exhibited the redeploy loop in
// the field: a remote-agent (remote-worktree) env installing the published
// runtime by reference from ghcr. It diverges from the runtime env only in
// `type`, so a loop here vs. a clean single-deploy for seedRuntimeForK3d
// isolates the loop to the remote-agent open path. The worktree is an (empty,
// init-populated) PVC — no in-pod git clone — so the pod deploys Ready without
// the SSH/worktree setup the create flow's `erun init` normally does.
export function seedRemoteAgentForK3d(tenant: string, environment: string, context: string): void {
  const envDir = path.join(erunConfigDir(), tenant, environment);
  fs.mkdirSync(envDir, { recursive: true });
  fs.writeFileSync(
    path.join(envDir, 'config.yaml'),
    `name: ${environment}\n` +
      `repopath: ${repoDir()}\n` +
      `kubernetescontext: ${context}\n` +
      'type: remote-agent\n' +
      'runtimeversion: 1.0.149\n' +
      'runtimeimage: ghcr.io/sophium/erun-devops\n' +
      'containerregistry: ghcr.io/sophium\n' +
      'aitool: sh\n',
  );
}

// seedGitRemoteAgentForK3d mirrors EXACTLY the config a real desktop git
// remote-agent create writes (captured from a live erun-k3s env): the load-bearing
// difference from seedRemoteAgentForK3d is `localrepopath` (the remote-worktree /
// git-checkout path) plus the persisted runtimeregistry and cluster registry. This
// lets a spec reproduce the git-create redeploy loop WITHOUT the per-env SSH-key
// import that blocks automation — the SSH wait only gates `erun init`; the loop is
// in the post-init deploy→open cycle, which this config exercises.
export function seedGitRemoteAgentForK3d(
  tenant: string,
  environment: string,
  context: string,
): void {
  const envDir = path.join(erunConfigDir(), tenant, environment);
  fs.mkdirSync(envDir, { recursive: true });
  fs.writeFileSync(
    path.join(envDir, 'config.yaml'),
    `name: ${environment}\n` +
      'type: remote-agent\n' +
      `localrepopath: /home/erun/git/${tenant}\n` +
      `repopath: ${repoDir()}\n` +
      `kubernetescontext: ${context}\n` +
      'runtimeversion: 1.0.149\n' +
      'runtimeregistry: ghcr.io/sophium\n' +
      'containerregistries:\n' +
      '    - cluster:\n' +
      '        service: erun-registry\n' +
      '        namespace: kube-system\n' +
      '        port: 5000\n' +
      '        insecure: true\n' +
      '      roles:\n' +
      '        - build\n' +
      '        - deploy\n' +
      'runtimeimage: ghcr.io/sophium/erun-devops\n' +
      'runtimepod:\n' +
      '    cpu: "1"\n' +
      '    memory: 4096Mi\n' +
      'aitool: sh\n',
  );
}

// writeStubBinary writes an inert POSIX stub.
//
// - erun: the desktop's ERun/AI tabs run `erun open …` from PATH. The real
//   CLI pointed at the seeded root would fail against the stubbed cluster
//   and the desktop's reconnect machinery would respawn it in a loop —
//   permanent activity churn and busy spinners across every spec. The stub
//   prints a shell-prompt line (the action runner's setup-complete marker,
//   see signalSessionReadyOnLine in erun-ui/activity_queue_app.go) and then
//   sleeps, so the tab behaves like a healthy opened session: alive, quiet,
//   and killable on env close.
// - kubectl: answers the context listing with an empty set (the env-init
//   dialog's deterministic empty state) and reports everything else as
//   unreachable.
function writeStubBinary(name: string): void {
  if (isWindows) {
    // CreateProcess cannot exec a shell script or a .cmd/.bat file, so copy the
    // prebuilt PE multi-call stub to <name>.exe. It dispatches on its own base
    // name — keep its behaviour in lockstep with the POSIX bodies below.
    fs.copyFileSync(winStubBinary(), stubBinPath(name));
    return;
  }
  let body: string;
  if (name === 'erun') {
    body = [
      '#!/bin/sh',
      '# erun playwright stub: keeps ERun/AI tabs alive and inert.',
      'case "$1" in',
      '  open)',
      "    printf 'erun@playwright:~$ \\n'",
      '    exec sleep 2147483647',
      '    ;;',
      '  *) exit 0 ;;',
      'esac',
      '',
    ].join('\n');
  } else if (name === 'kubectl') {
    body = [
      '#!/bin/sh',
      '# erun playwright stub for kubectl: no cluster in the isolated harness.',
      'case "$*" in',
      '  *"config get-contexts"*) exit 0 ;;',
      '  *) echo "kubectl stub: no cluster in the Playwright harness" >&2; exit 1 ;;',
      'esac',
      '',
    ].join('\n');
  } else {
    body = [
      '#!/bin/sh',
      `# erun playwright stub for ${name}: disabled in the isolated harness.`,
      `echo "${name} stub: disabled in the Playwright harness" >&2`,
      'exit 1',
      '',
    ].join('\n');
  }
  fs.writeFileSync(path.join(stubsDir(), name), body, { mode: 0o755 });
}

// seedBaseline writes the deterministic default-mode config tree every spec
// asserts against.
export function seedBaseline(): void {
  const root = erunConfigDir();
  fs.mkdirSync(path.join(root, SEED_TENANT), { recursive: true });
  fs.writeFileSync(
    path.join(root, 'config.yaml'),
    `defaulttenant: ${SEED_TENANT}\n` +
      'cloudproviders:\n' +
      `  - alias: ${SEED_CLOUD_ALIAS}\n` +
      '    provider: aws\n' +
      `    profile: ${SEED_CLOUD_ALIAS}\n` +
      `  - alias: ${SEED_CLOUDFLARE_ALIAS}\n` +
      '    provider: cloudflare\n' +
      '    username: pw-token\n' +
      '    accountid: 0123456789abcdef\n' +
      '    cloudflare:\n' +
      '      accountid: 0123456789abcdef\n' +
      '      tokenname: pw-token\n' +
      `      tokenref: cloudflare/${SEED_CLOUDFLARE_ALIAS}\n` +
      'orchestrators:\n' +
      `  - id: ${SEED_ORCHESTRATOR}\n` +
      `    name: ${SEED_ORCHESTRATOR}\n` +
      '    environments:\n' +
      `      - tenant: ${SEED_TENANT}\n` +
      `        environment: ${SEED_ENV_ALPHA}\n` +
      `        directory: ${repoDir()}\n`,
  );
  fs.writeFileSync(
    path.join(root, SEED_TENANT, 'config.yaml'),
    `projectroot: ${repoDir()}\n` +
      `name: ${SEED_TENANT}\n` +
      `defaultenvironment: ${SEED_ENV_ALPHA}\n` +
      'cloudprovideraliases:\n' +
      `  - ${SEED_CLOUD_ALIAS}\n` +
      `  - ${SEED_CLOUDFLARE_ALIAS}\n`,
  );
  seedEnvironment(SEED_TENANT, SEED_ENV_ALPHA);
  // beta additionally links the seeded cloud alias so the Manage dialog's
  // "clear a configured alias" contract is stageable (manage-cloud-alias-
  // clear.spec.ts). The alias has no cloud context behind it, so it stays
  // inert.
  seedEnvironment(SEED_TENANT, SEED_ENV_BETA, `cloudprovideralias: ${SEED_CLOUD_ALIAS}\n`);
  // gamma links both an AWS alias (legacy scalar) and a Cloudflare alias
  // (per-type map) so the per-provider-type env selectors are stageable.
  seedEnvironment(
    SEED_TENANT,
    SEED_ENV_GAMMA,
    `cloudprovideralias: ${SEED_CLOUD_ALIAS}\n` +
      'cloudprovideraliases:\n' +
      `  cloudflare: ${SEED_CLOUDFLARE_ALIAS}\n`,
  );
}

// seedBaselineForK3d seeds the pw tenant but NO environments: the e2e spec
// creates its own env at the live cluster, and the inert `test-context`
// baseline envs would fail real kubectl status checks.
export function seedBaselineForK3d(): void {
  const root = erunConfigDir();
  fs.mkdirSync(path.join(root, SEED_TENANT), { recursive: true });
  fs.writeFileSync(path.join(root, 'config.yaml'), `defaulttenant: ${SEED_TENANT}\n`);
  fs.writeFileSync(
    path.join(root, SEED_TENANT, 'config.yaml'),
    `projectroot: ${repoDir()}\n` + `name: ${SEED_TENANT}\n`,
  );
}

// seedEnvironment writes one inert local-agent env config, mirroring
// erun-integration/internal/fixture.SeedTenantEnv's env tree plus the explicit
// type (badge contract) and the inert `sh` AI tool.
export function seedEnvironment(tenant: string, environment: string, extraYaml = ''): void {
  const envDir = path.join(erunConfigDir(), tenant, environment);
  fs.mkdirSync(envDir, { recursive: true });
  fs.writeFileSync(
    path.join(envDir, 'config.yaml'),
    `name: ${environment}\n` +
      `repopath: ${repoDir()}\n` +
      'kubernetescontext: test-context\n' +
      'containerregistry: registry.example/test\n' +
      'runtimeversion: 1.0.0\n' +
      'type: local-agent\n' +
      'aitool: sh\n' +
      extraYaml,
  );
}

// seedRuntimeEnvironment writes an inert runtime-type env config. A runtime env
// is RemoteRepo (its worktree lives outside the local filesystem), so
// ResolveDeployableComponents offers the publishable platform components by
// reference — the sourceless deploy path — instead of local charts.
export function seedRuntimeEnvironment(tenant: string, environment: string, extraYaml = ''): void {
  const envDir = path.join(erunConfigDir(), tenant, environment);
  fs.mkdirSync(envDir, { recursive: true });
  fs.writeFileSync(
    path.join(envDir, 'config.yaml'),
    `name: ${environment}\n` +
      `repopath: /home/erun/git/${tenant}\n` +
      'kubernetescontext: test-context\n' +
      'containerregistry: registry.example/test\n' +
      'runtimeversion: 1.0.0\n' +
      'type: runtime\n' +
      'aitool: sh\n' +
      extraYaml,
  );
}

// seedHostEnvironment writes an inert host-type env config: a worktree with no
// pod and no cluster at all, so unlike seedEnvironment it carries no
// kubernetescontext, containerregistry, or runtimeversion — none of those
// apply to a type with no pod.
export function seedHostEnvironment(tenant: string, environment: string, extraYaml = ''): void {
  const envDir = path.join(erunConfigDir(), tenant, environment);
  fs.mkdirSync(envDir, { recursive: true });
  fs.writeFileSync(
    path.join(envDir, 'config.yaml'),
    `name: ${environment}\n` +
      `repopath: ${repoDir()}\n` +
      'type: host\n' +
      'aitool: sh\n' +
      extraYaml,
  );
}

// removeEnvironment deletes a previously seeded env config dir. The
// backend's fsnotify config watcher picks the deletion up and drops the
// sidebar row.
export function removeEnvironment(tenant: string, environment: string): void {
  fs.rmSync(path.join(erunConfigDir(), tenant, environment), { recursive: true, force: true });
}

// activityLeaseDir is where eruncommon.TakeEnvironmentActivityLease persists
// leases: XDG_CACHE_HOME/erun/activity/<tenant>/<environment>/leases.
function activityLeaseDir(tenant: string, environment: string): string {
  return path.join(isolatedHomeDir(), '.cache', 'erun', 'activity', tenant, environment, 'leases');
}

// writeHeldLease stages a real activity lease file — the same on-disk shape
// eruncommon.TakeEnvironmentActivityLease writes — so a headless spec can
// drive the AI-tab occupancy check (erun#1221) without a live orchestrator job.
export function writeHeldLease(tenant: string, environment: string, name: string): void {
  const dir = activityLeaseDir(tenant, environment);
  fs.mkdirSync(dir, { recursive: true });
  const startedAt = new Date();
  const expiresAt = new Date(startedAt.getTime() + 15 * 60 * 1000);
  fs.writeFileSync(
    path.join(dir, `${name}.json`),
    JSON.stringify({
      id: name,
      name,
      startedAt: startedAt.toISOString(),
      expiresAt: expiresAt.toISOString(),
    }),
  );
}

export function removeHeldLease(tenant: string, environment: string, name: string): void {
  fs.rmSync(path.join(activityLeaseDir(tenant, environment), `${name}.json`), { force: true });
}

// seedTenant writes the minimal tenant config.yaml ListTenantConfigs needs to
// surface a tenant at all — a tenant dir with no config.yaml is skipped as
// uninitialized. Mirrors what `erun init` writes (createTenantConfig in
// erun-common/init.go).
export function seedTenant(tenant: string, defaultEnvironment: string): void {
  const tenantDir = path.join(erunConfigDir(), tenant);
  fs.mkdirSync(tenantDir, { recursive: true });
  fs.writeFileSync(
    path.join(tenantDir, 'config.yaml'),
    `name: ${tenant}\n` + `defaultenvironment: ${defaultEnvironment}\n`,
  );
}

// removeTenant deletes a previously seeded tenant config dir (the tenant and
// all of its environments). The backend's fsnotify config watcher picks the
// deletion up and drops the sidebar rows.
export function removeTenant(tenant: string): void {
  fs.rmSync(path.join(erunConfigDir(), tenant), { recursive: true, force: true });
}

// removeIsolatedRoot deletes the whole suite-owned root. Only roots the
// suite recognizably created are removed, so a caller-provided custom path
// is never destroyed by accident.
export function removeIsolatedRoot(): void {
  const root = process.env.ERUN_PLAYWRIGHT_HOME?.trim();
  if (!root || !path.basename(root).startsWith('erun-playwright-home')) {
    return;
  }
  if (isWindows) {
    // A ConPTY stub/shell child spawned with its cwd inside the root (or a
    // still-blocking erun.exe stub from a session the app has not torn down yet)
    // keeps an OS handle on the tree, and Windows refuses to delete a directory
    // with open handles. Kill anything whose image lives under the isolated
    // stubs dir first so the delete below can succeed.
    try {
      const like = `${root.replace(/\\/g, '\\\\')}%`;
      execFileSync(
        'powershell',
        [
          '-NoProfile',
          '-Command',
          `Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -like '${like}' } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }`,
        ],
        { stdio: 'ignore' },
      );
    } catch {
      // best effort — the retry/tolerate below is the real backstop
    }
  }
  try {
    // maxRetries/retryDelay ride out a transient EPERM/EBUSY while a just-killed
    // handle is released (Windows).
    fs.rmSync(root, { recursive: true, force: true, maxRetries: 10, retryDelay: 200 });
  } catch (err) {
    // The root is a throwaway dir under the OS temp dir; run.sh's EXIT trap and
    // OS temp reclamation are the backstops. A cleanup that loses a race with a
    // not-yet-torn-down session must not fail an otherwise-green run.
    console.warn(`removeIsolatedRoot: could not fully remove ${root}: ${(err as Error).message}`);
  }
}

// uniqueEnvironmentName derives a per-test env name from the spec title so
// concurrent or repeated runs never collide.
export function uniqueEnvironmentName(title: string): string {
  const slug =
    title
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '')
      .slice(0, 16)
      .replace(/-+$/, '') || 'spec';
  const random = Math.random().toString(36).slice(2, 6);
  return `${slug}-${random}`;
}
