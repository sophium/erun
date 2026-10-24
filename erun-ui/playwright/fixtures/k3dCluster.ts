import { execFileSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as path from 'node:path';

import { isolatedRoot, kubeconfigPath } from './seedRoot.js';

// Real-cluster lifecycle for the opt-in k3d e2e mode (ERUN_E2E_K3D=1).
// Host preconditions are in erun-ui/playwright/AGENTS.md.

export interface K3dCluster {
  clusterName: string;
  registryName: string;
  context: string;
  // Reachable from both the host and the cluster nodes.
  registry: string;
}

const STATE_FILE = 'k3d-cluster.json';

function statePath(): string {
  return path.join(isolatedRoot(), STATE_FILE);
}

function run(bin: string, args: string[]): string {
  return execFileSync(bin, args, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'inherit'] });
}

export function createK3dCluster(): K3dCluster {
  // k3d registry names must start with `k3d-`.
  const suffix = Math.random().toString(36).slice(2, 8);
  const clusterName = `erun-e2e-${suffix}`;
  const registryName = `k3d-erun-e2e-reg-${suffix}`;
  run('k3d', [
    'cluster',
    'create',
    clusterName,
    '--registry-create',
    `${registryName}:0`,
    '--wait',
    '--kubeconfig-update-default=false',
    '--kubeconfig-switch-context=false',
  ]);

  const kubeconfig = run('k3d', ['kubeconfig', 'get', clusterName]);
  const kubePath = kubeconfigPath();
  fs.mkdirSync(path.dirname(kubePath), { recursive: true });
  fs.writeFileSync(kubePath, kubeconfig, { mode: 0o600 });

  const cluster: K3dCluster = {
    clusterName,
    registryName,
    context: `k3d-${clusterName}`,
    registry: resolveRegistryHostPort(registryName),
  };
  fs.writeFileSync(statePath(), JSON.stringify(cluster, null, 2));
  return cluster;
}

function resolveRegistryHostPort(registryName: string): string {
  const raw = run('k3d', ['registry', 'list', '-o', 'json']);
  const entries = JSON.parse(raw) as Array<{
    name?: string;
    portMappings?: Record<string, Array<{ HostIp?: string; HostPort?: string }>>;
  }>;
  const entry = entries.find(
    (candidate) => candidate.name === registryName || candidate.name === `/${registryName}`,
  );
  const mapping = entry?.portMappings?.['5000/tcp']?.[0];
  const port = mapping?.HostPort;
  if (!port) {
    throw new Error(`could not resolve host port for k3d registry ${registryName}`);
  }
  return `localhost:${port}`;
}

// e2eRealClusterContext names an already-running cluster to drive (ERUN_E2E_REAL_CLUSTER),
// or "" to create a fresh k3d one.
export function e2eRealClusterContext(): string {
  return (process.env.ERUN_E2E_REAL_CLUSTER ?? '').trim();
}

// useExistingCluster targets a running cluster; nothing is created or torn down.
export function useExistingCluster(context: string): K3dCluster {
  const kubeconfig = run('kubectl', ['config', 'view', '--raw']);
  const kubePath = kubeconfigPath();
  fs.mkdirSync(path.dirname(kubePath), { recursive: true });
  fs.writeFileSync(kubePath, kubeconfig, { mode: 0o600 });
  const cluster: K3dCluster = {
    clusterName: context,
    registryName: '',
    context,
    registry: 'ghcr.io/sophium',
  };
  fs.writeFileSync(statePath(), JSON.stringify(cluster, null, 2));
  return cluster;
}

export function readK3dCluster(): K3dCluster {
  return JSON.parse(fs.readFileSync(statePath(), 'utf8')) as K3dCluster;
}

// deleteK3dCluster is best-effort.
export function deleteK3dCluster(): void {
  // Never tear down a developer's real, pre-existing cluster (erun-k3s).
  if (e2eRealClusterContext() !== '') {
    return;
  }
  let cluster: K3dCluster | null = null;
  try {
    cluster = readK3dCluster();
  } catch {
    return;
  }
  try {
    run('k3d', ['cluster', 'delete', cluster.clusterName]);
  } catch {
    /* best-effort */
  }
  try {
    run('k3d', ['registry', 'delete', cluster.registryName]);
  } catch {
    /* best-effort */
  }
}
