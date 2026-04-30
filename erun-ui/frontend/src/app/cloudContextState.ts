import { StartCloudContext, StopCloudContext } from '../../wailsjs/go/main/App';
import { defaultCloudContextInitInput } from './state';
import { normalizeDialogValue } from './versionSuggestions';
import type { IdleCloudContextAction } from './model';
import type { UICloudContextInitInput, UICloudContextStatus, UICloudProviderStatus, UIERunConfig, UIIdleStatus } from '@/types';

export function idleCloudContextAction(idleStatus: UIIdleStatus | null, busy: boolean): IdleCloudContextAction | null {
  const name = normalizeDialogValue(idleStatus?.cloudContextName || '');
  if (!idleStatus || !idleStatus.managedCloud || !name || busy) {
    return null;
  }
  const running = normalizeDialogValue(idleStatus.cloudContextStatus || '').toLowerCase() === 'running';
  return {
    idleStatus,
    name,
    run: running ? StopCloudContext : StartCloudContext,
    label: running ? 'Stopped' : 'Started',
    refreshKubernetesContexts: !running,
  };
}

export function replaceCloudProvider(providers: UICloudProviderStatus[], provider: UICloudProviderStatus): UICloudProviderStatus[] {
  const next = providers.filter((item) => item.alias !== provider.alias);
  next.push(provider);
  next.sort((left, right) => left.alias.localeCompare(right.alias));
  return next;
}

export function replaceCloudContext(contexts: UICloudContextStatus[], context: UICloudContextStatus): UICloudContextStatus[] {
  const next = contexts.filter((item) => item.name !== context.name);
  next.push(context);
  next.sort((left, right) => left.name.localeCompare(right.name));
  return next;
}

export function cloudContextDraftForConfig(config: UIERunConfig, current: UICloudContextInitInput): UICloudContextInitInput {
  const draft = {
    ...defaultCloudContextInitInput(),
    ...current,
  };
  const providers = config.cloudProviders || [];
  if (!draft.cloudProviderAlias || !providers.some((provider) => provider.alias === draft.cloudProviderAlias)) {
    draft.cloudProviderAlias = providers[0]?.alias || '';
  }
  return draft;
}
