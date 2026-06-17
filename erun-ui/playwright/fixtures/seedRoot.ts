import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

// seedRoot owns the suite's isolated config root (issue #483). The headless
// backend — and every `erun` child process it spawns — runs against a
// throwaway HOME under this root, so the suite never reads or writes the
// developer's real ~/.erun / ~/.config/erun, and every machine sees the same
// deterministic baseline.
//
// Layout mirrors erun-integration/internal/env.New:
//
//   <root>/home/                 → HOME
//   <root>/home/.config/         → XDG_CONFIG_HOME
//   <root>/home/.cache/          → XDG_CACHE_HOME
//   <root>/home/.local/share/    → XDG_DATA_HOME
//   <root>/repo/                 → repopath / projectroot of the seeded envs
//   <root>/stubs/                → kubectl/helm/docker/aws stubs (PATH-prepended)
//
// The seeded config tree mirrors erun-integration/internal/fixture.SeedTenantEnv
// — keep the two in lockstep when a config field becomes load-bearing — plus
// two deliberate extensions: `type: local-agent` (the badge/type contract the
// sidebar specs assert) and `aitool: sh` (the AI tab launches an inert shell
// instead of a real claude/codex process).

// Baseline names every spec can rely on. One tenant, two inert local-agent
// environments. `alpha` sorts (and renders) first; `beta` exists so flows
// that need two envs under one tenant (back-to-back opens) are stageable.
export const SEED_TENANT = 'pw';
export const SEED_ENV_ALPHA = 'alpha';
export const SEED_ENV_BETA = 'beta';
// One configured cloud provider alias so the Manage dialog's Cloud alias
// select renders deterministically. The matching `aws` stub keeps its token
// status check instant and offline.
export const SEED_CLOUD_ALIAS = 'pw-aws';

// isolatedRoot resolves the suite-owned root directory. run.sh exports
// ERUN_PLAYWRIGHT_HOME; when the suite is launched without it (direct
// `playwright test`), a fresh temp root is created once in the runner
// process and propagated to workers through process.env.
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

// backendEnv returns the environment overrides for the `erun-app --headless`
// webServer process. HOME + XDG_* redirect both config roots (xdg.ConfigHome
// + "erun" and os.UserHomeDir() + ".erun") into the isolated root; the PATH
// prepend routes kubectl/helm/docker/aws — for the backend and for every
// `erun`/shell child it spawns — to the inert stubs so no real cluster,
// cloud, or docker daemon is ever touched.
export function backendEnv(): Record<string, string> {
  const home = isolatedHomeDir();
  return {
    HOME: home,
    XDG_CONFIG_HOME: path.join(home, '.config'),
    XDG_CACHE_HOME: path.join(home, '.cache'),
    XDG_DATA_HOME: path.join(home, '.local', 'share'),
    PATH: `${stubsDir()}${path.delimiter}${process.env.PATH ?? ''}`,
  };
}

// createIsolatedLayout materializes the directory layout and the stub
// binaries. Refuses to operate on anything that could be a real home.
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
  for (const name of ['kubectl', 'helm', 'docker', 'aws', 'erun']) {
    writeStubBinary(name);
  }
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
// - helm/docker/aws: fail fast unconditionally.
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

// seedBaseline writes the deterministic config tree the specs assert
// against: root config (default tenant + the one cloud provider), the pw
// tenant, and the alpha/beta environments.
export function seedBaseline(): void {
  const root = erunConfigDir();
  fs.mkdirSync(path.join(root, SEED_TENANT), { recursive: true });
  fs.writeFileSync(
    path.join(root, 'config.yaml'),
    `defaulttenant: ${SEED_TENANT}\n` +
      'cloudproviders:\n' +
      `  - alias: ${SEED_CLOUD_ALIAS}\n` +
      '    provider: aws\n' +
      `    profile: ${SEED_CLOUD_ALIAS}\n`,
  );
  fs.writeFileSync(
    path.join(root, SEED_TENANT, 'config.yaml'),
    `projectroot: ${repoDir()}\n` +
      `name: ${SEED_TENANT}\n` +
      `defaultenvironment: ${SEED_ENV_ALPHA}\n` +
      'cloudprovideraliases:\n' +
      `  - ${SEED_CLOUD_ALIAS}\n`,
  );
  seedEnvironment(SEED_TENANT, SEED_ENV_ALPHA);
  // beta additionally links the seeded cloud alias so the Manage dialog's
  // "clear a configured alias" contract is stageable (manage-cloud-alias-
  // clear.spec.ts). The alias has no cloud context behind it, so it stays
  // inert.
  seedEnvironment(SEED_TENANT, SEED_ENV_BETA, `cloudprovideralias: ${SEED_CLOUD_ALIAS}\n`);
}

// seedEnvironment writes one inert local-agent env config. Mirrors
// erun-integration/internal/fixture.SeedTenantEnv's env tree, plus the
// explicit type (badge contract) and the inert AI tool. extraYaml, when
// given, is appended verbatim to the env config.
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

// seedTenant writes a brand-new tenant's config.yaml (name + default
// environment) — the minimum ListTenantConfigs needs to surface the tenant
// at all (a tenant dir with no config.yaml is skipped as uninitialized).
// Mirrors what `erun init` writes for a new tenant (see createTenantConfig in
// erun-common/init.go). Pair with seedEnvironment to add the tenant's
// environments, and removeTenant to clean up afterwards.
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

// uniqueEnvironmentName derives a per-test throwaway env name from the spec
// title: a short slug plus a random suffix so concurrent or repeated runs
// never collide.
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
