import type { UIEnvironmentConfig, UISelection } from '@/types';

import { LoadEnvironmentConfig } from '../../wailsjs/go/main/App';
import { environmentTypeIsHost, environmentTypeIsRemoteWorktree } from './environmentType';
import type { RootState } from './store';

// Decides whether opening an env may spawn the ERun tab (which can start a
// stopped cloud host), answering locally when possible.

export type AutoStartGateVerdict = 'proceed' | 'skip-erun' | 'prompt';

export async function resolveAutoStartGate(
  selection: UISelection,
  getState: () => RootState,
): Promise<AutoStartGateVerdict> {
  const env = findTenantEnvironment(getState(), selection);
  const autoStartPolicy = env?.autoStart;
  // Host envs have nothing for `erun open` to enter; resolved here because a
  // rejected tab spawn is swallowed silently.
  if (environmentTypeIsHost(env?.type)) {
    return 'skip-erun';
  }
  if (!environmentTypeIsRemoteWorktree(env?.type) || autoStartPolicy === true) {
    return 'proceed';
  }
  const wouldStart = await wouldClickStartCloudContext(selection);
  if (!wouldStart) {
    return 'proceed';
  }
  return autoStartPolicy === false ? 'skip-erun' : 'prompt';
}

function findTenantEnvironment(state: RootState, selection: UISelection) {
  const tenant = state.tenants.tenants.find((item) => item.name === selection.tenant);
  return tenant?.environments.find((item) => item.name === selection.environment);
}

async function wouldClickStartCloudContext(selection: UISelection): Promise<boolean> {
  try {
    const config = (await LoadEnvironmentConfig(selection)) as UIEnvironmentConfig;
    const status = config.cloudContext?.status ?? '';
    return status !== '' && wouldAutoStartCloudContext(status);
  } catch {
    // On read failure, take the normal path so the real error surfaces.
    return false;
  }
}

function wouldAutoStartCloudContext(status: string): boolean {
  const normalized = status.trim().toLowerCase();
  return normalized !== 'running' && normalized !== 'starting';
}
