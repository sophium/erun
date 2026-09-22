import { Input, Label, SelectField } from 'erun-kit';
import * as React from 'react';

import {
  useClearGatewayCredentialMutation,
  useGetGatewayCredentialStatusQuery,
  useLoadGatewayModelsMutation,
  useSaveGatewayCredentialMutation,
} from '@/app/api/globalConfigApi';
import { readError } from '@/app/errors';
import { updateGlobalConfigDialog } from '@/app/globalConfigThunks';
import { useAppDispatch } from '@/app/hooks';
import type { AppState } from '@/app/state';
import type { AppDispatch } from '@/app/store';
import { selectableClaudeModelIds } from '@/components/app/claudeModels.helpers';
import { GatewayCredentialFields } from '@/components/app/GlobalConfigDialog.OpenRouterCredentials';
import { OpenRouterModelsField } from '@/components/app/GlobalConfigDialog.OpenRouterModels';
import type { UIGatewayModel, UIOpenRouterConfig, UIOpenRouterModel } from '@/uiOpenRouterTypes';

type GlobalConfigDialog = AppState['globalConfigDialog'];

// catalogEditor returns the section's edit helper.
//
// Every catalog edit derives its next value from the config as it stands in the
// store, not from the render closure it was created in. `updateGlobalConfig`
// merges at dispatch time, but the value handed to it is built by the caller —
// so two edits made within one render both start from the same snapshot and the
// second overwrites the first. Removing two model rows in quick succession is
// the case that bites: the second removal filters a list that still contains the
// first row and puts it back. Reading current state inside the thunk is what
// makes each edit compose with the one before it.
//
// The action is dispatched rather than returned: redux-thunk runs a thunk's body
// but never dispatches what it returns, so returning it would leave every edit
// as a function nobody calls.
function catalogEditor(dispatch: AppDispatch) {
  return (change: (current: UIOpenRouterConfig) => Partial<UIOpenRouterConfig>): void => {
    dispatch((innerDispatch, getState) => {
      const dialog = getState().globalConfigDialog;
      if (dialog.busy || dialog.configLoading) {
        return;
      }
      const current = dialog.config.openRouter ?? {};
      innerDispatch(
        updateGlobalConfigDialog({
          error: '',
          config: { ...dialog.config, openRouter: { ...current, ...change(current) } },
        }),
      );
    });
  };
}

// Known Anthropic-compatible gateway endpoints. The operator picks one rather
// than recalling a URL, and a self-hosted gateway stays reachable through the
// other option, because its address is genuinely theirs to supply.
const gatewayPresets: readonly { value: string; label: string }[] = [
  { value: 'https://openrouter.ai/api', label: 'OpenRouter' },
];

const gatewayNotConfigured = '__not_configured__';
const gatewayCustom = '__custom__';

// OpenRouterSection edits the erun-level gateway catalog: one list the operator
// maintains, which every environment then selects from. The credential is one
// erun-level value too — this machine's own Claude Code key unless the operator
// sets a different one — so no token value lands in config, and erun delivers
// it into each environment that uses the gateway.
export function OpenRouterSection({ dialog }: { dialog: GlobalConfigDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  const disabled = dialog.busy || dialog.configLoading;
  const gateway = dialog.config.openRouter ?? {};
  const models = gateway.models ?? [];

  const [loadGatewayModels, { isLoading: modelsLoading }] = useLoadGatewayModelsMutation();
  const [candidates, setCandidates] = React.useState<UIGatewayModel[]>([]);
  const [modelsError, setModelsError] = React.useState('');

  // Read rather than inferred: the catalog reports the key a deploy would
  // actually deliver, which is this machine's own when nothing else is saved.
  const { data: credentialStatus } = useGetGatewayCredentialStatusQuery(undefined);
  const [saveGatewayCredential, { isLoading: savingCredential }] =
    useSaveGatewayCredentialMutation();
  const [clearGatewayCredential, { isLoading: clearingCredential }] =
    useClearGatewayCredentialMutation();

  const editCatalog = catalogEditor(dispatch);

  const patch = (values: Partial<UIOpenRouterConfig>) => {
    editCatalog(() => values);
  };

  const setModel = (index: number, values: Partial<UIOpenRouterModel>) => {
    editCatalog((current) => ({
      models: (current.models ?? []).map((model, i) =>
        i === index ? { ...model, ...values } : model,
      ),
    }));
  };

  const removeModel = (index: number) => {
    editCatalog((current) => ({ models: (current.models ?? []).filter((_, i) => i !== index) }));
  };

  const addModel = () => {
    editCatalog((current) => ({
      models: [...(current.models ?? []), { id: '', context: undefined }],
    }));
  };

  // The gateway publishes the models it serves, so an operator picks rather
  // than types. Fetched on demand because it reads a base URL they may still be
  // editing, and a gateway without the endpoint is a supported configuration:
  // the rows stay typeable and the reason is named beside the button.
  const fetchCandidates = async () => {
    setModelsError('');
    try {
      setCandidates(await loadGatewayModels(gateway.baseUrl ?? '').unwrap());
    } catch (error) {
      setCandidates([]);
      setModelsError(readError(error));
    }
  };

  // Only ids that are usable as a launched model are offered as the default:
  // defaulting to a blank or malformed row would launch an id the guard drops.
  const selectable = selectableClaudeModelIds(models);
  const gatewayConfigured = (gateway.baseUrl ?? '').trim() !== '';

  return (
    <div className="grid gap-3">
      <GatewayEndpointFields gateway={gateway} disabled={disabled} patch={patch} />

      {/* Only once a gateway is chosen: a key for a gateway that does not exist
          is a field that changes nothing. */}
      {gatewayConfigured ? (
        <GatewayCredentialFields
          status={credentialStatus}
          disabled={disabled}
          busy={savingCredential || clearingCredential}
          onSave={(token) => {
            void saveGatewayCredential(token);
          }}
          onClear={() => {
            void clearGatewayCredential(undefined);
          }}
        />
      ) : null}

      <OpenRouterModelsField
        models={models}
        candidates={candidates}
        disabled={disabled}
        onAddModel={addModel}
        onSetModel={setModel}
        onRemoveModel={removeModel}
        onFetchCandidates={() => void fetchCandidates()}
        fetchingCandidates={modelsLoading}
        canFetchCandidates={!disabled && (gateway.baseUrl ?? '').trim() !== ''}
        candidatesError={modelsError}
      />

      <SelectField
        id="global-config-openrouter-default-model"
        label="Default model"
        value={gateway.defaultModel ?? 'default'}
        options={[
          { value: 'default', label: 'First model in the list' },
          ...selectable.map((id) => ({ value: id, label: id })),
        ]}
        helper="The model an environment selects when it has not chosen one."
        disabled={disabled}
        onChange={(next) => {
          patch({ defaultModel: next === 'default' ? undefined : next });
        }}
      />
    </div>
  );
}

function GatewayEndpointFields({
  gateway,
  disabled,
  patch,
}: {
  gateway: UIOpenRouterConfig;
  disabled?: boolean;
  patch: (values: Partial<UIOpenRouterConfig>) => void;
}): React.ReactElement {
  const baseUrl = (gateway.baseUrl ?? '').trim();
  const preset = gatewayPresets.find((entry) => entry.value === baseUrl);
  // Self-hosted is held in state as well as derived, because choosing it clears
  // the URL: without this the selection would fall back to Not configured the
  // moment the operator picked it, taking the field away mid-entry.
  const [selfHosted, setSelfHosted] = React.useState(false);
  const selection =
    baseUrl === ''
      ? selfHosted
        ? gatewayCustom
        : gatewayNotConfigured
      : (preset?.value ?? gatewayCustom);

  return (
    <>
      <div className="grid gap-2">
        <SelectField
          id="global-config-openrouter-gateway"
          label="Gateway"
          value={selection}
          options={[
            { value: gatewayNotConfigured, label: 'Not configured' },
            ...gatewayPresets.map((entry) => ({
              value: entry.value,
              label: `${entry.label} — ${entry.value}`,
            })),
            { value: gatewayCustom, label: 'Self-hosted (enter a URL)' },
          ]}
          helper="The Anthropic-compatible endpoint every environment's Claude Code is routed through. Not configured keeps environments on their own Claude sign-in."
          disabled={disabled}
          onChange={(next) => {
            if (next === gatewayCustom) {
              setSelfHosted(true);
              // Cleared so the operator types their own rather than editing the
              // preset they just moved away from.
              patch({ baseUrl: '' });
              return;
            }
            setSelfHosted(false);
            patch({ baseUrl: next === gatewayNotConfigured ? '' : next });
          }}
        />
      </div>
      {selection === gatewayCustom ? (
        <div className="grid gap-2">
          <Label htmlFor="global-config-openrouter-baseurl">Gateway base URL</Label>
          <Input
            id="global-config-openrouter-baseurl"
            autoComplete="off"
            value={gateway.baseUrl ?? ''}
            disabled={disabled}
            placeholder="https://gateway.example.com/anthropic"
            onChange={(event) => {
              patch({ baseUrl: event.target.value });
            }}
          />
        </div>
      ) : null}
    </>
  );
}
