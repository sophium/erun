import type { UIEnvironmentConfig, UISelection } from '@/types';

import { LoadEnvironmentConfig } from '../../wailsjs/go/main/App';
import type { RootState } from './store';

// autoStartGate decides whether openSelection should let the ERun tab
// spawn (and therefore let erun open's CloudContextPreflight start EC2).
// The gate only triggers a Wails round-trip in the cases where the answer
// is not already known from state.tenants, so the common autoStart=true
// path stays click-to-spawn with no extra latency.

export type AutoStartGateVerdict = 'proceed' | 'skip-erun' | 'prompt';

export async function resolveAutoStartGate(
  selection: UISelection,
  getState: () => RootState,
): Promise<AutoStartGateVerdict> {
  const env = findTenantEnvironment(getState(), selection);
  const autoStartPolicy = env?.autoStart;
  if (!env?.remote || autoStartPolicy === true) {
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
    // Best-effort: if the env config read fails the gate cannot tell
    // whether opening would start EC2. Treat as "not starting" so the
    // normal open path runs and the underlying error surfaces in the
    // terminal area instead of being swallowed by a silent skip.
    return false;
  }
}

// wouldAutoStartCloudContext returns true when the linked cloud context is
// in a state that would cause erun open's CloudContextPreflight to issue an
// EC2 start. "running" and "starting" already imply a hot or starting host,
// so the desktop has nothing to prompt about; everything else (stopped,
// stopping, unknown) is treated as "starting will happen, ask the user".
function wouldAutoStartCloudContext(status: string): boolean {
  const normalized = status.trim().toLowerCase();
  return normalized !== 'running' && normalized !== 'starting';
}
