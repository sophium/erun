import type {
  UICloudContextInitInput,
  UICloudContextStatus,
  UICloudProviderStatus,
  UIERunConfig,
  UIIdleStatus,
} from '@/types';

import { StartCloudContext, StopCloudContext } from '../../wailsjs/go/main/App';
import { cloudNodeIsRunning } from './cloudNodeStatus';
import type { CloudNodeOperation, IdleCloudContextAction } from './model';
import { defaultCloudContextInitInput } from './state';
import { normalizeDialogValue } from './versionSuggestions';

// idleCloudContextAction resolves what clicking the titlebar's power control
// would do. It reads the same two inputs idleCloudAction renders — the node's
// current state, and the operation already in flight against that node — so the
// control's label and the action behind it cannot describe different things.
// The two used to read one global `busy` flag with opposite meanings ("no
// action available" here, "my action is in progress" there); the flag is now the
// per-node operation record, and both ask it the same question.
export function idleCloudContextAction(
  idleStatus: UIIdleStatus | null,
  inFlight: CloudNodeOperation | null,
): IdleCloudContextAction | null {
  const name = normalizeDialogValue(idleStatus?.cloudContextName ?? '');
  if (!idleStatus?.managedCloud || !name || inFlight) {
    return null;
  }
  if (cloudNodeIsRunning(idleStatus.cloudContextStatus)) {
    return {
      idleStatus,
      operation: 'stop',
      name,
      run: StopCloudContext,
      label: 'Stopped',
      refreshKubernetesContexts: false,
    };
  }
  return {
    idleStatus,
    operation: 'start',
    name,
    run: StartCloudContext,
    label: 'Started',
    refreshKubernetesContexts: true,
  };
}

export function replaceCloudProvider(
  providers: UICloudProviderStatus[],
  provider: UICloudProviderStatus,
): UICloudProviderStatus[] {
  const next = providers.filter((item) => item.alias !== provider.alias);
  next.push(provider);
  next.sort((left, right) => left.alias.localeCompare(right.alias));
  return next;
}

export function replaceCloudContext(
  contexts: UICloudContextStatus[],
  context: UICloudContextStatus,
): UICloudContextStatus[] {
  const next = contexts.filter((item) => item.name !== context.name);
  next.push(context);
  next.sort((left, right) => left.name.localeCompare(right.name));
  return next;
}

export function cloudContextDraftForConfig(
  config: UIERunConfig,
  current: UICloudContextInitInput,
): UICloudContextInitInput {
  const draft = {
    ...defaultCloudContextInitInput(),
    ...current,
  };
  const providers = config.cloudProviders ?? [];
  if (
    !draft.cloudProviderAlias ||
    !providers.some((provider) => provider.alias === draft.cloudProviderAlias)
  ) {
    draft.cloudProviderAlias = providers[0]?.alias ?? '';
  }
  return draft;
}
