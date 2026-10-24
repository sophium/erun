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

// Each edit reads current store state inside the thunk, so two edits in one
// render compose instead of the second overwriting the first. The action is
// dispatched because redux-thunk does not dispatch a thunk's return value.
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

const gatewayPresets: readonly { value: string; label: string }[] = [
  { value: 'https://openrouter.ai/api', label: 'OpenRouter' },
];

const gatewayNotConfigured = '__not_configured__';
const gatewayCustom = '__custom__';

// OpenRouterSection edits the erun-level gateway catalog. No token lands in
// config; erun delivers the credential to each environment.
export function OpenRouterSection({ dialog }: { dialog: GlobalConfigDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  const disabled = dialog.busy || dialog.configLoading;
  const gateway = dialog.config.openRouter ?? {};
  const models = gateway.models ?? [];

  const [loadGatewayModels, { isLoading: modelsLoading }] = useLoadGatewayModelsMutation();
  const [candidates, setCandidates] = React.useState<UIGatewayModel[]>([]);
  const [modelsError, setModelsError] = React.useState('');

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

  // Fetched on demand; a gateway without the models endpoint is supported and
  // the rows stay typeable.
  const fetchCandidates = async () => {
    setModelsError('');
    try {
      setCandidates(await loadGatewayModels(gateway.baseUrl ?? '').unwrap());
    } catch (error) {
      setCandidates([]);
      setModelsError(readError(error));
    }
  };

  // Blank or malformed ids would be dropped at launch, so they are not offered.
  const selectable = selectableClaudeModelIds(models);
  const gatewayConfigured = (gateway.baseUrl ?? '').trim() !== '';

  return (
    <div className="grid gap-3">
      <GatewayEndpointFields gateway={gateway} disabled={disabled} patch={patch} />

      {/* A key without a gateway changes nothing. */}
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
  // Held in state because choosing self-hosted clears the URL, which would
  // otherwise derive back to Not configured.
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
