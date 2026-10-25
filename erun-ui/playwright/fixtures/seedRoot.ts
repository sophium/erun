import { execFileSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

import {
  STUB_REGISTRY_FILENAME,
  STUBS_DIRNAME,
  reapStubProcessesBeneath,
} from './stubProcesses.js';

// Windows cannot exec shell-script stubs, so win32 uses .exe stubs built from fixtures/winstub.
const isWindows = process.platform === 'win32';

// The suite's isolated config root: a throwaway HOME, so no spec touches the real ~/.erun.
// Layout mirrors erun-integration's env.New and fixture.SeedTenantEnv; keep them in lockstep.

// Baseline names every spec can rely on.
export const SEED_TENANT = 'pw';
export const SEED_ENV_ALPHA = 'alpha';
export const SEED_ENV_BETA = 'beta';
// gamma attaches both an AWS and a Cloudflare alias, one selector per provider type.
export const SEED_ENV_GAMMA = 'gamma';
// delta is hosted on a platform row; not in seedBaseline because specs count the baseline
// population. Staged per test by seedHostedEnvironment.
export const SEED_ENV_DELTA = 'delta';
export const SEED_HOSTED_API_HOST = 'api.example.test';
export const SEED_HOSTED_TENANT_ID = 'seeded-platform-tenant';
export const SEED_HOSTED_ENVIRONMENT_ID = 'seeded-platform-env';
export const SEED_HOSTED_REVISION = 4;
// Any non-matching digest reads as "moved since its last transfer".
export const SEED_HOSTED_STALE_DIGEST = 'digest-of-an-earlier-revision';
export const SEED_CLOUD_ALIAS = 'pw-aws';
// No token is stored, so this alias reads "not_configured" offline.
export const SEED_CLOUDFLARE_ALIAS = 'pw-token+0123456789abcdef@cloudflare';
// A persisted, stopped orchestrator linking alpha.
export const SEED_ORCHESTRATOR = 'pw-orch';

// workerRootOverride is per worker process (see fixtures/workerBackend.ts).
let workerRootOverride: string | undefined;

export function setWorkerRoot(root: string): void {
  workerRootOverride = root;
}

// isolatedRoot: worker override, then ERUN_PLAYWRIGHT_HOME, else a fresh temp root cached
// back into the env so forked workers agree.
export function isolatedRoot(): string {
  if (workerRootOverride) {
    return workerRootOverride;
  }
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

export function stubsDir(): string {
  return path.join(isolatedRoot(), STUBS_DIRNAME);
}

// stubRegistryPath is where long-lived stubs record their pids for reaping (stubProcesses.ts).
export function stubRegistryPath(): string {
  return path.join(isolatedRoot(), STUB_REGISTRY_FILENAME);
}

// openerLogPath records each OS-opener stub request, one line per call.
export function openerLogPath(): string {
  return path.join(isolatedRoot(), 'opener-calls');
}

export function openerCalls(): string[] {
  try {
    return fs.readFileSync(openerLogPath(), 'utf8').split('\n').filter(Boolean);
  } catch {
    return [];
  }
}

// claudeAgentsPath stands in for Claude Code's agent service: `<id> <run|stopped> <name>` per line.
export function claudeAgentsPath(): string {
  return path.join(isolatedRoot(), 'claude-agents');
}

export interface ClaudeStubAgent {
  id: string;
  running: boolean;
  name: string;
}

export function claudeAgents(): ClaudeStubAgent[] {
  let text = '';
  try {
    text = fs.readFileSync(claudeAgentsPath(), 'utf8');
  } catch {
    return [];
  }
  return text
    .split('\n')
    .filter(Boolean)
    .map((line) => {
      const [id = '', state = '', name = ''] = line.split(' ');
      return { id, running: state === 'run', name };
    });
}

// seedClaudeAgent stages an agent the desktop did not create.
export function seedClaudeAgent(agent: ClaudeStubAgent): void {
  fs.appendFileSync(
    claudeAgentsPath(),
    `${agent.id} ${agent.running ? 'run' : 'stopped'} ${agent.name}\n`,
  );
}

export function tabSetupHoldsDir(): string {
  return path.join(isolatedRoot(), 'tab-setup-holds');
}

// holdTabSetup keeps the env's tabs in setup until released; release it in a finally.
export function holdTabSetup(tenant: string, environment: string): () => void {
  const dir = tabSetupHoldsDir();
  fs.mkdirSync(dir, { recursive: true });
  const hold = path.join(dir, `${tenant}.${environment}`);
  fs.writeFileSync(hold, '');
  return () => {
    fs.rmSync(hold, { force: true });
  };
}

function stubBinPath(name: string): string {
  return path.join(stubsDir(), isWindows ? `${name}.exe` : name);
}

// winStubBinary builds the multi-call PE once per run.
let winStubBuilt: string | undefined;
function winStubBinary(): string {
  if (winStubBuilt) {
    return winStubBuilt;
  }
  const out = path.join(stubsDir(), '_winstub.exe');
  const src = path.join(__dirname, 'winstub');
  // GOWORK=off so a stray go.work cannot shadow this nested module.
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

let atomicWriteCounter = 0;

// writeConfigFile writes atomically, like base/config's WriteFileAtomic, so the fsnotify-driven
// reader never sees a torn file.
function writeConfigFile(filePath: string, content: string): void {
  const dir = path.dirname(filePath);
  const tmp = path.join(
    dir,
    `.${path.basename(filePath)}.tmp-${process.pid}-${atomicWriteCounter++}`,
  );
  fs.writeFileSync(tmp, content);
  fs.renameSync(tmp, filePath);
}

function erunConfigDir(): string {
  return path.join(isolatedHomeDir(), '.config', 'erun');
}

// e2eK3dEnabled gates every real-cluster branch; only for Docker + k3d + binfmt hosts.
export function e2eK3dEnabled(): boolean {
  return process.env.ERUN_E2E_K3D === '1';
}

// desktopConfigDir mirrors Go's os.UserConfigDir() + "ERun" for the backend's environment.
export function desktopConfigDir(): string {
  const home = isolatedHomeDir();
  if (process.platform === 'darwin') {
    return path.join(home, 'Library', 'Application Support', 'ERun');
  }
  if (process.platform === 'win32') {
    return path.join(windowsRoamingAppData(home), 'ERun');
  }
  return path.join(home, '.config', 'ERun');
}

// On Windows Go reads %AppData%/%LocalAppData%/%USERPROFILE%, never HOME or XDG_*.
function windowsRoamingAppData(home: string): string {
  return path.join(home, 'AppData', 'Roaming');
}

function windowsLocalAppData(home: string): string {
  return path.join(home, 'AppData', 'Local');
}

export function kubeconfigPath(): string {
  return path.join(isolatedHomeDir(), '.kube', 'config');
}

// backendEnv isolates both erun roots and puts the stubs first on PATH.
export function backendEnv(): Record<string, string> {
  const home = isolatedHomeDir();
  const base: Record<string, string> = {
    HOME: home,
    XDG_CONFIG_HOME: path.join(home, '.config'),
    XDG_CACHE_HOME: path.join(home, '.cache'),
    XDG_DATA_HOME: path.join(home, '.local', 'share'),
    ...(process.platform === 'win32'
      ? {
          USERPROFILE: home,
          APPDATA: windowsRoamingAppData(home),
          LOCALAPPDATA: windowsLocalAppData(home),
        }
      : {}),
    ERUN_PLAYWRIGHT_STUB_REGISTRY: stubRegistryPath(),
    ERUN_PLAYWRIGHT_TAB_SETUP_HOLDS: tabSetupHoldsDir(),
    ERUN_PLAYWRIGHT_OPENER_LOG: openerLogPath(),
    ERUN_PLAYWRIGHT_CLAUDE_AGENTS: claudeAgentsPath(),
  };
  if (e2eK3dEnabled()) {
    // k3d mode uses real tools; only `aws` stays stubbed.
    return {
      ...base,
      PATH: `${stubsDir()}${path.delimiter}${process.env.PATH ?? ''}`,
      KUBECONFIG: kubeconfigPath(),
      ERUN_AWS_BIN: stubBinPath('aws'),
      ERUN_APP_CLI: process.env.ERUN_E2E_ERUN_BIN ?? 'erun',
    };
  }
  return {
    ...base,
    PATH: `${stubsDir()}${path.delimiter}${process.env.PATH ?? ''}`,
    // PATH alone is not enough: resolveCLIExecutable prefers a dev-built erun next to the app binary.
    ERUN_APP_CLI: stubBinPath('erun'),
    // Deterministic per-version chart availability for the Runtime tab (see deploy_components.go).
    // 1.0.50 is deliberately empty: a blocked deploy.
    ERUN_CHART_AVAILABILITY_OVERRIDE:
      '1.0.0=erun-devops,pw-backend-postgres,pw-backend-db,pw-backend-api,pw-powerdns,pw-docs;' +
      '1.0.90=erun-devops,pw-backend-postgres,pw-backend-db;1.0.50=;' +
      // ERun-line versions the picker specs deploy.
      '1.0.134=erun-devops;1.0.16=erun-devops',
    // Seeded ports must never read as reachable: they can collide with a real env's forwards.
    ERUN_LOCAL_PORT_REACHABILITY_OVERRIDE: '0',
    // The hosted-registry probe is a real network call page.route cannot intercept.
    ERUN_HOSTED_REGISTRY_PROBE_OVERRIDE: '0',
    // An rc-free shell with LOCAL_SHELL_PROMPT, so terminal specs see a known prompt.
    ERUN_LOCAL_SHELL_OVERRIDE: '1',
  };
}

// LOCAL_SHELL_PROMPT matches session.go's localShellDeterministicPrompt; specs that select
// text by position wait for it first.
export const LOCAL_SHELL_PROMPT = 'erun-test$ ';

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
  // Openers are always stubbed so a spec never opens a real file on the desktop.
  const openers = isWindows ? ['explorer'] : ['open', 'xdg-open'];
  const stubs = e2eK3dEnabled()
    ? ['aws', ...openers]
    : ['kubectl', 'helm', 'docker', 'aws', 'erun', 'claude', ...openers];
  for (const name of stubs) {
    writeStubBinary(name);
  }
}

// seedEnvironmentForK3d has no runtimeversion, forcing a full build → push → deploy.
export function seedEnvironmentForK3d(
  tenant: string,
  environment: string,
  context: string,
  registry: string,
): void {
  const envDir = path.join(erunConfigDir(), tenant, environment);
  fs.mkdirSync(envDir, { recursive: true });
  writeConfigFile(
    path.join(envDir, 'config.yaml'),
    `name: ${environment}\n` +
      `repopath: ${repoDir()}\n` +
      `kubernetescontext: ${context}\n` +
      `containerregistry: ${registry}\n` +
      'type: local-agent\n' +
      'aitool: sh\n',
  );
}

// seedRuntimeForK3d installs the public ghcr runtime by reference, skipping the multi-arch build.
export function seedRuntimeForK3d(tenant: string, environment: string, context: string): void {
  const envDir = path.join(erunConfigDir(), tenant, environment);
  fs.mkdirSync(envDir, { recursive: true });
  writeConfigFile(
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

// seedRemoteAgentForK3d differs from seedRuntimeForK3d only in `type`, isolating the
// remote-agent open path.
export function seedRemoteAgentForK3d(tenant: string, environment: string, context: string): void {
  const envDir = path.join(erunConfigDir(), tenant, environment);
  fs.mkdirSync(envDir, { recursive: true });
  writeConfigFile(
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

// seedGitRemoteAgentForK3d is the config a real git remote-agent create writes, minus the
// SSH-key import that blocks automation.
export function seedGitRemoteAgentForK3d(
  tenant: string,
  environment: string,
  context: string,
): void {
  const envDir = path.join(erunConfigDir(), tenant, environment);
  fs.mkdirSync(envDir, { recursive: true });
  writeConfigFile(
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

// stubRegisterPreamble records the pid; the later `exec` keeps it, so it is the parked process.
function stubRegisterPreamble(name: string): string[] {
  return [
    '#!/bin/sh',
    'register_stub() {',
    '  [ -n "$ERUN_PLAYWRIGHT_STUB_REGISTRY" ] || return 0',
    `  printf '%s ${name}\\n' "$$" >> "$ERUN_PLAYWRIGHT_STUB_REGISTRY"`,
    '}',
  ];
}

// writeStubBinary writes an inert POSIX stub; keep in lockstep with fixtures/winstub/main.go.
// - erun: prints the setup-complete marker and sleeps, so tabs stay alive without reconnect
//   churn; `--no-shell` probes exit because callers (e.g. orchestrator launch) wait on them.
// - kubectl: an empty context listing; everything else unreachable.
// - claude: answers background-agent commands from claudeAgentsPath(); attach and transient
//   sessions block, since an orchestrator reads "running" only while its session is live.
// Long-lived stubs register their pid first so the harness can reap them (stubProcesses.ts).
function writeStubBinary(name: string): void {
  if (isWindows) {
    fs.copyFileSync(winStubBinary(), stubBinPath(name));
    return;
  }
  let body: string;
  if (name === 'erun') {
    body = [
      ...stubRegisterPreamble(name),
      '# erun playwright stub: keeps ERun/AI tabs alive and inert.',
      'case "$*" in',
      '  *--no-shell*) exit 0 ;;',
      'esac',
      'case "$1" in',
      '  open)',
      '    register_stub',
      '    hold="$ERUN_PLAYWRIGHT_TAB_SETUP_HOLDS/$2.$3"',
      '    while [ -n "$ERUN_PLAYWRIGHT_TAB_SETUP_HOLDS" ] && [ -e "$hold" ]; do sleep 0.05; done',
      "    printf 'erun@playwright:~$ \\n'",
      '    exec sleep 2147483647',
      '    ;;',
      '  *) exit 0 ;;',
      'esac',
      '',
    ].join('\n');
  } else if (name === 'claude') {
    body = [
      ...stubRegisterPreamble(name),
      '# claude playwright stub: an inert background service and session.',
      'agents="${ERUN_PLAYWRIGHT_CLAUDE_AGENTS:-$HOME/.claude-playwright-agents}"',
      'touch "$agents"',
      'set_state() {',
      '  tmp="$agents.$$"',
      '  while read -r id state name; do',
      '    [ "$id" = "$1" ] && state="$2"',
      '    [ "$2" = gone ] && [ "$id" = "$1" ] && continue',
      '    printf \'%s %s %s\\n\' "$id" "$state" "$name"',
      '  done < "$agents" > "$tmp"',
      '  mv "$tmp" "$agents"',
      '}',
      'case "$1" in',
      '  agents)',
      '    sep=""',
      "    printf '['",
      '    while read -r id state name; do',
      '      pid=""',
      '      [ "$state" = run ] && pid=\'"pid":1,\'',
      '      printf \'%s{%s"id":"%s","kind":"background","name":"%s","sessionId":"%s-0000-4000-8000-000000000000"}\' "$sep" "$pid" "$id" "$name" "$id"',
      '      sep=","',
      '    done < "$agents"',
      "    printf ']\\n'",
      '    exit 0 ;;',
      '  --bg)',
      '    name=""',
      '    while [ $# -gt 0 ]; do [ "$1" = -n ] && name="$2"; shift; done',
      "    id=$(printf '%08x' $$)",
      '    printf \'%s run %s\\n\' "$id" "$name" >> "$agents"',
      '    printf \'backgrounded · %s · %s\\n\' "$id" "$name"',
      '    exit 0 ;;',
      '  stop) set_state "$2" stopped; printf \'stopped %s\\n\' "$2"; exit 0 ;;',
      '  rm) set_state "$2" gone; printf \'removed %s\\n\' "$2"; exit 0 ;;',
      '  attach) set_state "$2" run ;;',
      'esac',
      'register_stub',
      "printf 'claude@playwright:~$ \\n'",
      'exec sleep 2147483647',
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
  } else if (name === 'open' || name === 'xdg-open') {
    body = [
      '#!/bin/sh',
      `# erun playwright stub for ${name}: records the request and opens nothing.`,
      '[ -n "$ERUN_PLAYWRIGHT_OPENER_LOG" ] && printf \'%s\\n\' "$*" >> "$ERUN_PLAYWRIGHT_OPENER_LOG"',
      'exit 0',
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

export function seedBaseline(): void {
  const root = erunConfigDir();
  fs.mkdirSync(path.join(root, SEED_TENANT), { recursive: true });
  writeConfigFile(
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
  writeConfigFile(
    path.join(root, SEED_TENANT, 'config.yaml'),
    `projectroot: ${repoDir()}\n` +
      `name: ${SEED_TENANT}\n` +
      `defaultenvironment: ${SEED_ENV_ALPHA}\n` +
      'cloudprovideraliases:\n' +
      `  - ${SEED_CLOUD_ALIAS}\n` +
      `  - ${SEED_CLOUDFLARE_ALIAS}\n`,
  );
  seedEnvironment(SEED_TENANT, SEED_ENV_ALPHA);
  // beta links the cloud alias for manage-cloud-alias-clear.spec.ts.
  seedEnvironment(SEED_TENANT, SEED_ENV_BETA, `cloudprovideralias: ${SEED_CLOUD_ALIAS}\n`);
  // gamma: legacy scalar AWS alias plus per-type Cloudflare map.
  seedEnvironment(
    SEED_TENANT,
    SEED_ENV_GAMMA,
    `cloudprovideralias: ${SEED_CLOUD_ALIAS}\n` +
      'cloudprovideraliases:\n' +
      `  cloudflare: ${SEED_CLOUDFLARE_ALIAS}\n`,
  );
}

// seedBaselineForK3d seeds no envs: inert baseline envs would fail real kubectl checks.
export function seedBaselineForK3d(): void {
  const root = erunConfigDir();
  fs.mkdirSync(path.join(root, SEED_TENANT), { recursive: true });
  writeConfigFile(path.join(root, 'config.yaml'), `defaulttenant: ${SEED_TENANT}\n`);
  writeConfigFile(
    path.join(root, SEED_TENANT, 'config.yaml'),
    `projectroot: ${repoDir()}\n` + `name: ${SEED_TENANT}\n`,
  );
}

// seedEnvironment writes one inert local-agent env (see fixture.SeedTenantEnv).
export function seedEnvironment(tenant: string, environment: string, extraYaml = ''): void {
  const envDir = path.join(erunConfigDir(), tenant, environment);
  fs.mkdirSync(envDir, { recursive: true });
  writeConfigFile(
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

// seedHostedEnvironment adds the hosted marker to an otherwise alpha-like env.
// Omitting definitionDigest reads as "cannot tell"; SEED_HOSTED_STALE_DIGEST as "moved".
export function seedHostedEnvironment(
  tenant: string,
  environment: string,
  options: { definitionDigest?: string; definitionRevision?: number } = {},
): void {
  seedEnvironment(
    tenant,
    environment,
    'hosted:\n' +
      `  apihost: ${SEED_HOSTED_API_HOST}\n` +
      `  tenantid: ${SEED_HOSTED_TENANT_ID}\n` +
      `  environmentid: ${SEED_HOSTED_ENVIRONMENT_ID}\n` +
      `  definitionrevision: ${options.definitionRevision ?? SEED_HOSTED_REVISION}\n` +
      (options.definitionDigest ? `  definitiondigest: ${options.definitionDigest}\n` : ''),
  );
}

// seedEnvironmentWithRuntimeVersions lets specs set the hover card's runtime version fields.
export function seedEnvironmentWithRuntimeVersions(
  tenant: string,
  environment: string,
  versions: {
    runtimeVersion?: string;
    runtimeRunningImage?: string;
    runtimeImage?: string;
    runtimeChart?: string;
  },
): void {
  const envDir = path.join(erunConfigDir(), tenant, environment);
  fs.mkdirSync(envDir, { recursive: true });
  const lines = [
    `name: ${environment}`,
    `repopath: ${repoDir()}`,
    'kubernetescontext: test-context',
    'containerregistry: registry.example/test',
    `runtimeversion: ${versions.runtimeVersion ?? '1.0.0'}`,
    'type: local-agent',
    'aitool: sh',
  ];
  if (versions.runtimeRunningImage) {
    lines.push(`runtimerunningimage: ${versions.runtimeRunningImage}`);
  }
  if (versions.runtimeImage) {
    lines.push(`runtimeimage: ${versions.runtimeImage}`);
  }
  if (versions.runtimeChart) {
    lines.push(`runtimechart: ${versions.runtimeChart}`);
  }
  writeConfigFile(path.join(envDir, 'config.yaml'), lines.join('\n') + '\n');
}

// seedRuntimeEnvironment is RemoteRepo, so components are offered by reference.
export function seedRuntimeEnvironment(tenant: string, environment: string, extraYaml = ''): void {
  const envDir = path.join(erunConfigDir(), tenant, environment);
  fs.mkdirSync(envDir, { recursive: true });
  writeConfigFile(
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

// seedHostEnvironment has no pod, so no cluster, registry or runtime fields.
export function seedHostEnvironment(tenant: string, environment: string, extraYaml = ''): void {
  const envDir = path.join(erunConfigDir(), tenant, environment);
  fs.mkdirSync(envDir, { recursive: true });
  writeConfigFile(
    path.join(envDir, 'config.yaml'),
    `name: ${environment}\n` +
      `repopath: ${repoDir()}\n` +
      'type: host\n' +
      'aitool: sh\n' +
      extraYaml,
  );
}

// removeEnvironment deletes an env dir; the backend's watcher drops the row.
export function removeEnvironment(tenant: string, environment: string): void {
  fs.rmSync(path.join(erunConfigDir(), tenant, environment), { recursive: true, force: true });
}

function environmentActivityDir(tenant: string, environment: string): string {
  return path.join(isolatedHomeDir(), '.cache', 'erun', 'activity', tenant, environment);
}

function activityLeaseDir(tenant: string, environment: string): string {
  return path.join(environmentActivityDir(tenant, environment), 'leases');
}

// writeTruncatedActivitySnapshot stages a present-but-empty snapshot. Use a kind the
// harness never records, or a real record replaces it first.
export function writeTruncatedActivitySnapshot(
  tenant: string,
  environment: string,
  kind: string,
): void {
  const dir = environmentActivityDir(tenant, environment);
  fs.mkdirSync(dir, { recursive: true });
  fs.writeFileSync(path.join(dir, `${kind}.json`), '');
}

export function removeActivitySnapshot(tenant: string, environment: string, kind: string): void {
  fs.rmSync(path.join(environmentActivityDir(tenant, environment), `${kind}.json`), {
    force: true,
  });
}

// writeHeldLease stages a real lease file, renamed into place because readers reclaim
// unparseable leases.
export function writeHeldLease(tenant: string, environment: string, name: string): void {
  const dir = activityLeaseDir(tenant, environment);
  fs.mkdirSync(dir, { recursive: true });
  const startedAt = new Date();
  const expiresAt = new Date(startedAt.getTime() + 15 * 60 * 1000);
  writeConfigFile(
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

function environmentJobDir(tenant: string, environment: string): string {
  return path.join(isolatedHomeDir(), '.cache', 'erun', 'activity', tenant, environment, 'jobs');
}

// writeCompletedJob stages a finished job; a terminal state skips PID liveness checks.
export function writeCompletedJob(
  tenant: string,
  environment: string,
  id: string,
  name: string,
): void {
  const dir = environmentJobDir(tenant, environment);
  fs.mkdirSync(dir, { recursive: true });
  const startedAt = new Date(Date.now() - 60_000);
  const endedAt = new Date();
  fs.writeFileSync(
    path.join(dir, `${id}.json`),
    JSON.stringify({
      id,
      name,
      state: 'exited',
      succeeded: true,
      exitCode: 0,
      startedAt: startedAt.toISOString(),
      endedAt: endedAt.toISOString(),
    }),
  );
}

export function removeCompletedJob(tenant: string, environment: string, id: string): void {
  fs.rmSync(path.join(environmentJobDir(tenant, environment), `${id}.json`), { force: true });
}

// seedTenant writes the minimal tenant config ListTenantConfigs needs, as `erun init` does.
export function seedTenant(tenant: string, defaultEnvironment: string, extraYaml = ''): void {
  const tenantDir = path.join(erunConfigDir(), tenant);
  fs.mkdirSync(tenantDir, { recursive: true });
  writeConfigFile(
    path.join(tenantDir, 'config.yaml'),
    `name: ${tenant}\n` + `defaultenvironment: ${defaultEnvironment}\n` + extraYaml,
  );
}

export function removeTenant(tenant: string): void {
  fs.rmSync(path.join(erunConfigDir(), tenant), { recursive: true, force: true });
}

const ORCHESTRATORS_KEY = 'orchestrators:';

function orchestratorEntries(
  ids: string[],
  tenant: string,
  environment: string,
  indent: number,
): string[] {
  const item = ' '.repeat(indent);
  const key = ' '.repeat(indent + 2);
  const nested = ' '.repeat(indent + 4);
  return ids.flatMap((id) => [
    `${item}- id: ${id}`,
    `${key}name: ${id}`,
    `${key}environments:`,
    `${nested}- tenant: ${tenant}`,
    `${nested}  environment: ${environment}`,
    `${nested}  directory: ${repoDir()}`,
  ]);
}

// withOrchestrators splices `ids` into the `orchestrators:` block at the file's own indent:
// the desktop's marshaller indents by four and may add keys after it, and a mis-indented
// append corrupts the file for the rest of the worker.
function withOrchestrators(
  source: string,
  ids: string[],
  tenant: string,
  environment: string,
): string {
  const lines = source.split('\n');
  const keyIndex = lines.findIndex((line) => line.trimEnd() === ORCHESTRATORS_KEY);
  if (keyIndex < 0) {
    // A new top-level key at EOF is valid whatever precedes it.
    const base = source === '' || source.endsWith('\n') ? source : `${source}\n`;
    return `${base}${ORCHESTRATORS_KEY}\n${orchestratorEntries(ids, tenant, environment, 2).join('\n')}\n`;
  }
  let end = keyIndex + 1;
  while (end < lines.length && /^\s+\S/.test(lines[end] ?? '')) {
    end += 1;
  }
  const existingItem = lines.slice(keyIndex + 1, end).find((line) => /^\s+- /.test(line));
  const indent = existingItem ? existingItem.length - existingItem.trimStart().length : 2;
  lines.splice(end, 0, ...orchestratorEntries(ids, tenant, environment, indent));
  return lines.join('\n');
}

// addOrchestrators stages throwaway orchestrator entries; returns a restore function.
export function addOrchestrators(ids: string[], tenant: string, environment: string): () => void {
  const configPath = path.join(erunConfigDir(), 'config.yaml');
  const before = fs.readFileSync(configPath, 'utf8');
  writeConfigFile(configPath, withOrchestrators(before, ids, tenant, environment));
  return () => {
    writeConfigFile(configPath, before);
  };
}

// orchestratorOpenStatePath is the desktop's open set (internal/orchestrator/open_state.go), separate
// from config.yaml.
function orchestratorOpenStatePath(): string {
  return path.join(desktopConfigDir(), 'orchestrator-open.json');
}

// Leaving an open-set entry without its config makes the next launch raise a sidebar
// role="alert" that breaks later specs' strict-mode alert locators.
function removeOrchestratorOpenState(id: string): void {
  const statePath = orchestratorOpenStatePath();
  let state: { orchestrators?: Array<{ orchestratorId?: string }> };
  try {
    state = JSON.parse(fs.readFileSync(statePath, 'utf8')) as typeof state;
  } catch {
    return;
  }
  const entries = state.orchestrators ?? [];
  const kept = entries.filter((entry) => entry.orchestratorId !== id);
  if (kept.length === entries.length) {
    return;
  }
  if (kept.length === 0) {
    // The desktop removes the file rather than writing an empty set.
    fs.rmSync(statePath, { force: true });
    return;
  }
  fs.writeFileSync(statePath, `${JSON.stringify({ ...state, orchestrators: kept })}\n`);
}

export function removeOrchestrator(id: string): void {
  // Before the config edit: an open-set entry can outlive its config entry.
  removeOrchestratorOpenState(id);
  const configPath = path.join(erunConfigDir(), 'config.yaml');
  const lines = fs.readFileSync(configPath, 'utf8').split('\n');
  const keyIndex = lines.findIndex((line) => line.trimEnd() === ORCHESTRATORS_KEY);
  if (keyIndex < 0) {
    return;
  }
  let blockEnd = keyIndex + 1;
  while (blockEnd < lines.length && /^\s+\S/.test(lines[blockEnd] ?? '')) {
    blockEnd += 1;
  }
  let itemStart = -1;
  let itemIndent = 0;
  for (let i = keyIndex + 1; i < blockEnd; i += 1) {
    const line = lines[i] ?? '';
    const trimmed = line.trimStart();
    if (trimmed === `- id: ${id}`) {
      itemStart = i;
      itemIndent = line.length - trimmed.length;
      break;
    }
  }
  if (itemStart < 0) {
    return;
  }
  let itemEnd = itemStart + 1;
  while (itemEnd < blockEnd) {
    const line = lines[itemEnd] ?? '';
    const trimmed = line.trimStart();
    const indent = line.length - trimmed.length;
    if (trimmed.startsWith('- ') && indent <= itemIndent) {
      break;
    }
    itemEnd += 1;
  }
  lines.splice(itemStart, itemEnd - itemStart);
  writeConfigFile(configPath, lines.join('\n'));
}

const CLOUD_PROVIDERS_KEY = 'cloudproviders:';

function erunCloudProviderEntry(alias: string, apiURL: string, indent: number): string[] {
  const item = ' '.repeat(indent);
  const key = ' '.repeat(indent + 2);
  const nested = ' '.repeat(indent + 4);
  return [
    `${item}- alias: ${alias}`,
    `${key}provider: erun`,
    `${key}erun:`,
    `${nested}apiurl: ${apiURL}`,
    `${nested}clientid: pw-erun-client`,
  ];
}

// withERunCloudProviderAlias splices like withOrchestrators, for the same reordering reason.
function withERunCloudProviderAlias(source: string, alias: string, apiURL: string): string {
  const lines = source.split('\n');
  const keyIndex = lines.findIndex((line) => line.trimEnd() === CLOUD_PROVIDERS_KEY);
  if (keyIndex < 0) {
    const base = source === '' || source.endsWith('\n') ? source : `${source}\n`;
    return `${base}${CLOUD_PROVIDERS_KEY}\n${erunCloudProviderEntry(alias, apiURL, 2).join('\n')}\n`;
  }
  let end = keyIndex + 1;
  while (end < lines.length && /^\s+\S/.test(lines[end] ?? '')) {
    end += 1;
  }
  const existingItem = lines.slice(keyIndex + 1, end).find((line) => /^\s+- /.test(line));
  const indent = existingItem ? existingItem.length - existingItem.trimStart().length : 2;
  lines.splice(end, 0, ...erunCloudProviderEntry(alias, apiURL, indent));
  return lines.join('\n');
}

// addERunCloudProviderAlias stages an erun-type alias with no credential; returns a restore function.
export function addERunCloudProviderAlias(alias: string, apiURL: string): () => void {
  const configPath = path.join(erunConfigDir(), 'config.yaml');
  const before = fs.readFileSync(configPath, 'utf8');
  writeConfigFile(configPath, withERunCloudProviderAlias(before, alias, apiURL));
  return () => {
    writeConfigFile(configPath, before);
  };
}

const OPENROUTER_KEY = 'openrouter:';

function withoutTopLevelBlock(lines: string[], key: string): string[] {
  const start = lines.findIndex((line) => line.trimEnd() === key);
  if (start < 0) {
    return lines;
  }
  let end = start + 1;
  while (end < lines.length && /^\s+\S/.test(lines[end] ?? '')) {
    end += 1;
  }
  return [...lines.slice(0, start), ...lines.slice(end)];
}

// setOpenRouterCatalog replaces the gateway catalog, appended as a top-level key; no
// credential is written. Returns a restore function.
export function setOpenRouterCatalog(catalog: {
  baseUrl: string;
  models: string[];
  defaultModel?: string;
}): () => void {
  const configPath = path.join(erunConfigDir(), 'config.yaml');
  const before = fs.readFileSync(configPath, 'utf8');
  const kept = withoutTopLevelBlock(before.split('\n'), OPENROUTER_KEY);
  while (kept.length > 0 && (kept[kept.length - 1] ?? '').trim() === '') {
    kept.pop();
  }
  const block = [
    OPENROUTER_KEY,
    `  baseurl: ${catalog.baseUrl}`,
    ...(catalog.defaultModel ? [`  defaultmodel: ${catalog.defaultModel}`] : []),
    ...(catalog.models.length > 0
      ? ['  models:', ...catalog.models.map((id) => `    - id: ${id}`)]
      : []),
  ];
  writeConfigFile(configPath, `${[...kept, ...block].join('\n')}\n`);
  return () => {
    writeConfigFile(configPath, before);
  };
}

// removeIsolatedRoot removes only roots the suite recognizably created.
export function removeIsolatedRoot(): void {
  const root = process.env.ERUN_PLAYWRIGHT_HOME?.trim();
  if (!root || !path.basename(root).startsWith('erun-playwright-home')) {
    return;
  }
  removeRootDir(root);
}

export function removeWorkerRoot(root: string): void {
  removeRootDir(root);
}

// removeRootDir reaps parked stubs first; deleting the root would lose their registry.
function removeRootDir(root: string): void {
  const survivors = reapStubProcessesBeneath(root);
  if (survivors.length > 0) {
    console.warn(
      `removeRootDir: stub processes ${survivors.join(', ')} under ${root} survived SIGKILL`,
    );
  }
  if (isWindows) {
    // Windows refuses to delete a tree with open handles, so kill stub processes under it first.
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
    // Retries ride out EPERM/EBUSY while a just-killed handle is released (Windows).
    fs.rmSync(root, { recursive: true, force: true, maxRetries: 10, retryDelay: 200 });
  } catch (err) {
    // A lost cleanup race must not fail a green run; OS temp reclamation is the backstop.
    console.warn(`removeIsolatedRoot: could not fully remove ${root}: ${(err as Error).message}`);
  }
}

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
