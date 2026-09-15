import { Button, Input, Label, SelectField } from 'erun-kit';
import { LoaderCircle, Plus, RefreshCw, Trash2 } from 'lucide-react';
import * as React from 'react';

import { useLoadGatewayModelsMutation } from '@/app/api/globalConfigApi';
import { readError } from '@/app/errors';
import { updateGlobalConfig } from '@/app/globalConfigThunks';
import { useAppDispatch } from '@/app/hooks';
import type { AppState } from '@/app/state';
import {
  isClaudeModelToken,
  selectableClaudeModelIds,
} from '@/components/app/claudeModels.helpers';
import type { UIOpenRouterConfig, UIOpenRouterModel } from '@/uiOpenRouterTypes';
import type { UIGatewayModel } from '@/uiOpenRouterTypes';

type GlobalConfigDialog = AppState['globalConfigDialog'];

const DEFAULT_TOKEN_KEY = 'token';
// Mirrors erun-common's DefaultOpenRouterAuthTokenSecret: one catalog means one
// Secret name for every environment, so it is defaulted rather than required.
const DEFAULT_TOKEN_KEY_SECRET = 'erun-claude-gateway';

// Known Anthropic-compatible gateway endpoints. The operator picks one rather
// than recalling a URL, and a self-hosted gateway stays reachable through the
// other option, because its address is genuinely theirs to supply.
const gatewayPresets: readonly { value: string; label: string }[] = [
  { value: 'https://openrouter.ai/api', label: 'OpenRouter' },
];

const gatewayNotConfigured = '__not_configured__';
const gatewayCustom = '__custom__';

// OpenRouterSection edits the erun-level gateway catalog: one list the operator
// maintains, which every environment then selects from. A credential is named
// by Secret rather than typed in, so no token value lands in config.
export function OpenRouterSection({ dialog }: { dialog: GlobalConfigDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  const disabled = dialog.busy || dialog.configLoading;
  const gateway = dialog.config.openRouter ?? {};
  const models = gateway.models ?? [];

  const [loadGatewayModels, { isLoading: modelsLoading }] = useLoadGatewayModelsMutation();
  const [candidates, setCandidates] = React.useState<UIGatewayModel[]>([]);
  const [modelsError, setModelsError] = React.useState('');

  const patch = (values: Partial<UIOpenRouterConfig>) => {
    dispatch(updateGlobalConfig({ openRouter: { ...gateway, ...values } }));
  };

  const setModel = (index: number, values: Partial<UIOpenRouterModel>) => {
    patch({ models: models.map((model, i) => (i === index ? { ...model, ...values } : model)) });
  };

  const removeModel = (index: number) => {
    patch({ models: models.filter((_, i) => i !== index) });
  };

  const addModel = () => {
    patch({ models: [...models, { id: '', context: undefined }] });
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

  return (
    <div className="grid gap-3">
      <GatewayEndpointFields gateway={gateway} disabled={disabled} patch={patch} />

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

      <div className="grid gap-2 sm:grid-cols-2">
        <div className="grid gap-2">
          <Label htmlFor="global-config-openrouter-secret">Credential Secret (optional)</Label>
          <Input
            id="global-config-openrouter-secret"
            autoComplete="off"
            value={gateway.authTokenSecret ?? ''}
            disabled={disabled}
            placeholder={DEFAULT_TOKEN_KEY_SECRET}
            onChange={(event) => {
              patch({ authTokenSecret: event.target.value });
            }}
          />
        </div>
        <div className="grid gap-2">
          <Label htmlFor="global-config-openrouter-secret-key">Secret key (optional)</Label>
          <Input
            id="global-config-openrouter-secret-key"
            autoComplete="off"
            value={gateway.authTokenKey ?? ''}
            disabled={disabled}
            placeholder={DEFAULT_TOKEN_KEY}
            onChange={(event) => {
              patch({ authTokenKey: event.target.value });
            }}
          />
        </div>
      </div>
      <div className="text-[12px] leading-[1.4] text-muted-foreground">
        A Kubernetes Secret in each environment&apos;s namespace holding the gateway token. One
        catalog means one name, so leaving either field empty uses {DEFAULT_TOKEN_KEY_SECRET} and
        its {DEFAULT_TOKEN_KEY} key — name them only to reuse a Secret you already manage. The value
        itself is never stored in config.
      </div>
    </>
  );
}

// OpenRouterModelsField edits the catalog rows: the model ids an environment may
// select, and each id's context window.
function OpenRouterModelsField({
  models,
  candidates,
  disabled,
  onAddModel,
  onSetModel,
  onRemoveModel,
  onFetchCandidates,
  fetchingCandidates,
  canFetchCandidates,
  candidatesError,
}: {
  models: UIOpenRouterModel[];
  candidates: UIGatewayModel[];
  disabled?: boolean;
  onAddModel: () => void;
  onSetModel: (index: number, values: Partial<UIOpenRouterModel>) => void;
  onRemoveModel: (index: number) => void;
  onFetchCandidates: () => void;
  fetchingCandidates: boolean;
  canFetchCandidates: boolean;
  candidatesError: string;
}): React.ReactElement {
  return (
    <div className="grid gap-2">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <Label>Models</Label>
        <div className="flex flex-wrap items-center gap-2">
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={!canFetchCandidates || fetchingCandidates}
            onClick={onFetchCandidates}
          >
            {fetchingCandidates ? (
              <LoaderCircle className="size-3.5 animate-spin" />
            ) : (
              <RefreshCw className="size-3.5" />
            )}
            {candidates.length > 0 ? 'Reload from gateway' : 'Load from gateway'}
          </Button>
          <Button
            type="button"
            variant="secondary"
            size="sm"
            disabled={disabled}
            onClick={onAddModel}
          >
            <Plus className="size-3.5" />
            Add model
          </Button>
        </div>
      </div>
      <div className="text-[12px] leading-[1.4] text-muted-foreground">
        {candidates.length > 0
          ? `Offering ${String(candidates.length)} models from the gateway. A window the gateway reports is filled in for you.`
          : 'Load the gateway’s own model list to pick ids, or type one and set its window.'}
      </div>
      {candidatesError === '' ? null : (
        <div className="text-[12px] leading-[1.4] text-destructive">{candidatesError}</div>
      )}
      {models.length === 0 ? (
        <div className="text-[12px] leading-[1.4] text-muted-foreground">
          No models yet. Add the gateway model ids an environment may select.
        </div>
      ) : (
        models.map((model, index) => (
          <OpenRouterModelRow
            key={index}
            model={model}
            index={index}
            candidates={candidates}
            disabled={disabled}
            onSetModel={onSetModel}
            onRemoveModel={onRemoveModel}
          />
        ))
      )}
      <div className="text-[12px] leading-[1.4] text-muted-foreground">
        A model&apos;s window must be the provider-level figure, which can be smaller than its
        advertised maximum. Leaving it empty makes Claude Code assume one.
      </div>
    </div>
  );
}

function OpenRouterModelRow({
  model,
  index,
  candidates,
  disabled,
  onSetModel,
  onRemoveModel,
}: {
  model: UIOpenRouterModel;
  index: number;
  candidates: UIGatewayModel[];
  disabled?: boolean;
  onSetModel: (index: number, values: Partial<UIOpenRouterModel>) => void;
  onRemoveModel: (index: number) => void;
}): React.ReactElement {
  const id = model.id.trim();
  const invalid = id !== '' && !isClaudeModelToken(id);
  const known = candidates.some((candidate) => candidate.id === id);
  // A gateway that reported its list bounds the choice to it; a stored id the
  // list no longer carries stays selectable so it can be seen and changed.
  const options = [
    ...(id !== '' && !known ? [{ value: id, label: `${id} (not offered by the gateway)` }] : []),
    ...candidates.map((candidate) => ({
      value: candidate.id,
      // A gateway that sends no display name leaves the id as the only label.
      label: candidate.displayName ? `${candidate.displayName} — ${candidate.id}` : candidate.id,
    })),
  ];
  return (
    <div
      data-openrouter-model={index}
      className="grid gap-2 sm:grid-cols-[1fr_10rem_auto] sm:items-start"
    >
      <div className="grid gap-1">
        {candidates.length === 0 ? (
          <Input
            aria-label={`Model id ${String(index + 1)}`}
            autoComplete="off"
            value={model.id}
            disabled={disabled}
            placeholder="deepseek/deepseek-v4.1-flash"
            aria-invalid={invalid}
            onChange={(event) => {
              onSetModel(index, { id: event.target.value });
            }}
          />
        ) : (
          <SelectField
            id={`global-config-openrouter-model-${String(index)}`}
            label=""
            value={id}
            options={options}
            emptyLabel="Select a model"
            disabled={disabled}
            onChange={(next) => {
              // Choosing from the gateway's list fills in the window it reports;
              // one that reports none leaves whatever is there for the operator.
              const picked = candidates.find((candidate) => candidate.id === next);
              onSetModel(index, {
                id: next,
                ...(picked?.context ? { context: picked.context } : {}),
              });
            }}
          />
        )}
        {invalid ? (
          <div className="text-[12px] leading-[1.4] text-destructive">
            A model id may contain only letters, digits, and . _ : / -
          </div>
        ) : null}
      </div>
      <Input
        aria-label={`Context window for model ${String(index + 1)}`}
        type="number"
        inputMode="numeric"
        min={1}
        step={1}
        autoComplete="off"
        value={model.context === undefined ? '' : String(model.context)}
        disabled={disabled}
        placeholder="Context window"
        onChange={(event) => {
          const next = event.target.value.trim();
          // An empty window is unknown rather than zero, so the launch leaves
          // Claude Code's own assumption in place.
          onSetModel(index, { context: next === '' ? undefined : Math.trunc(Number(next)) });
        }}
      />
      <Button
        type="button"
        variant="ghost"
        size="sm"
        aria-label={`Remove model ${String(index + 1)}`}
        disabled={disabled}
        onClick={() => {
          onRemoveModel(index);
        }}
      >
        <Trash2 className="size-3.5" />
      </Button>
    </div>
  );
}
