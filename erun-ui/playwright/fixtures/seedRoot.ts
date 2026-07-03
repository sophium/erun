import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

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
      ERUN_AWS_BIN: path.join(stubsDir(), 'aws'),
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
    ERUN_APP_CLI: path.join(stubsDir(), 'erun'),
  };
}

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
      `      tokenref: cloudflare/${SEED_CLOUDFLARE_ALIAS}\n`,
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

// removeEnvironment deletes a previously seeded env config dir. The
// backend's fsnotify config watcher picks the deletion up and drops the
// sidebar row.
export function removeEnvironment(tenant: string, environment: string): void {
  fs.rmSync(path.join(erunConfigDir(), tenant, environment), { recursive: true, force: true });
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
  fs.rmSync(root, { recursive: true, force: true });
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
