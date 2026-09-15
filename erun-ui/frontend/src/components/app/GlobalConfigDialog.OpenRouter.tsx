import {
  Button,
  cn,
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  Input,
  Label,
  Popover,
  PopoverContent,
  PopoverTrigger,
  SelectField,
} from 'erun-kit';
import { Check, ChevronsUpDown, LoaderCircle, Plus, RefreshCw, Trash2 } from 'lucide-react';
import * as React from 'react';

import {
  useClearGatewayCredentialMutation,
  useGetGatewayCredentialStatusQuery,
  useLoadGatewayModelsMutation,
  useSaveGatewayCredentialMutation,
} from '@/app/api/globalConfigApi';
import { readError } from '@/app/errors';
import { updateGlobalConfig } from '@/app/globalConfigThunks';
import { useAppDispatch } from '@/app/hooks';
import type { AppState } from '@/app/state';
import {
  isClaudeModelToken,
  selectableClaudeModelIds,
} from '@/components/app/claudeModels.helpers';
import { GatewayCredentialFields } from '@/components/app/GlobalConfigDialog.OpenRouterCredentials';
import type { UIGatewayModel, UIOpenRouterConfig, UIOpenRouterModel } from '@/uiOpenRouterTypes';

type GlobalConfigDialog = AppState['globalConfigDialog'];

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
  return (
    <div
      data-openrouter-model={index}
      className="grid gap-2 sm:grid-cols-[1fr_10rem_auto] sm:items-start"
    >
      <div className="grid gap-1">
        <div className="relative">
          {/* The field stays typeable as well as pickable: a gateway serves ids
              it does not list, and refusing one would leave no way to reach it.
              The list is searched rather than scrolled, because a gateway can
              serve hundreds of models. */}
          <Input
            aria-label={`Model id ${String(index + 1)}`}
            className={candidates.length > 0 ? 'pr-10' : undefined}
            autoComplete="off"
            value={model.id}
            disabled={disabled}
            placeholder="deepseek/deepseek-v4.1-flash"
            aria-invalid={invalid}
            onChange={(event) => {
              onSetModel(index, { id: event.target.value });
            }}
          />
          {candidates.length > 0 ? (
            <GatewayModelChoices
              index={index}
              selectedId={id}
              candidates={candidates}
              disabled={disabled}
              onPick={(candidate) => {
                // A window the gateway reports comes with the pick; one that
                // reports none leaves whatever is there.
                onSetModel(index, {
                  id: candidate.id,
                  ...(candidate.context ? { context: candidate.context } : {}),
                });
              }}
            />
          ) : null}
        </div>
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

// GatewayModelChoices is the searchable list of what the gateway serves. It is
// searched rather than scrolled because a gateway can serve hundreds of models,
// and a flat list of them is unusable.
function GatewayModelChoices({
  index,
  selectedId,
  candidates,
  disabled,
  onPick,
}: {
  index: number;
  selectedId: string;
  candidates: UIGatewayModel[];
  disabled?: boolean;
  onPick: (candidate: UIGatewayModel) => void;
}): React.ReactElement {
  const [open, setOpen] = React.useState(false);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          className="absolute right-1 top-1 size-7 text-muted-foreground"
          type="button"
          variant="ghost"
          size="icon"
          aria-label={`Show gateway models for model ${String(index + 1)}`}
          disabled={disabled === true}
        >
          <ChevronsUpDown />
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-96 p-0" align="start" collisionPadding={12}>
        <Command>
          <CommandInput placeholder="Search models..." />
          <CommandList>
            <CommandEmpty>No model matches.</CommandEmpty>
            <CommandGroup>
              {candidates.map((candidate) => (
                <GatewayModelItem
                  key={candidate.id}
                  candidate={candidate}
                  selected={candidate.id === selectedId}
                  onSelect={() => {
                    setOpen(false);
                    onPick(candidate);
                  }}
                />
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}

// GatewayModelItem is one searchable choice: the name the gateway gives it, the
// id it actually launches with, and the window that comes with the pick. The
// entry's value carries both name and id so a search matches either — an
// operator may know the model by its name and the launch needs the id.
function GatewayModelItem({
  candidate,
  selected,
  onSelect,
}: {
  candidate: UIGatewayModel;
  selected: boolean;
  onSelect: () => void;
}): React.ReactElement {
  const name =
    candidate.displayName === '' ? candidate.id : (candidate.displayName ?? candidate.id);
  return (
    <CommandItem
      className="min-w-0"
      value={`${candidate.displayName ?? ''} ${candidate.id}`}
      onSelect={onSelect}
    >
      <Check className={cn('size-4 shrink-0 opacity-0', selected && 'opacity-100')} />
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="truncate text-sm font-medium leading-tight">{name}</span>
        <span className="truncate text-xs leading-tight text-muted-foreground">
          {candidate.context
            ? `${candidate.id} | ${String(candidate.context)} tokens`
            : candidate.id}
        </span>
      </span>
    </CommandItem>
  );
}
