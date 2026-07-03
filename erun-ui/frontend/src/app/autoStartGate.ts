import type { UIEnvironmentConfig, UISelection } from '@/types';

import { LoadEnvironmentConfig } from '../../wailsjs/go/main/App';
import { environmentTypeIsRemoteWorktree } from './environmentType';
import type { RootState } from './store';

// The gate decides whether opening an env may spawn the ERun tab — which is what
// lets erun open start a stopped EC2 host — so the desktop can prompt before
// incurring a cloud start. It skips the backend round-trip whenever local state
// already answers, keeping the common path instant.

export type AutoStartGateVerdict = 'proceed' | 'skip-erun' | 'prompt';

export async function resolveAutoStartGate(
  selection: UISelection,
  getState: () => RootState,
): Promise<AutoStartGateVerdict> {
  const env = findTenantEnvironment(getState(), selection);
  const autoStartPolicy = env?.autoStart;
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
    // On read failure, fall back to the normal open path so the real error
    // surfaces to the user instead of being hidden behind a silent skip.
    return false;
  }
}

function wouldAutoStartCloudContext(status: string): boolean {
  const normalized = status.trim().toLowerCase();
  return normalized !== 'running' && normalized !== 'starting';
}
