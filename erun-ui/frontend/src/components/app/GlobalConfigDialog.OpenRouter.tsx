import { Button, Input, Label, SelectField } from 'erun-kit';
import { Plus, Trash2 } from 'lucide-react';
import * as React from 'react';

import { updateGlobalConfig } from '@/app/globalConfigThunks';
import { useAppDispatch } from '@/app/hooks';
import type { AppState } from '@/app/state';
import {
  isClaudeModelToken,
  selectableClaudeModelIds,
} from '@/components/app/claudeModels.helpers';
import type { UIOpenRouterConfig, UIOpenRouterModel } from '@/uiOpenRouterTypes';

type GlobalConfigDialog = AppState['globalConfigDialog'];

const DEFAULT_TOKEN_KEY = 'token';

// OpenRouterSection edits the erun-level gateway catalog: one list the operator
// maintains, which every environment then selects from. A credential is named
// by Secret rather than typed in, so no token value lands in config.
export function OpenRouterSection({ dialog }: { dialog: GlobalConfigDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  const disabled = dialog.busy || dialog.configLoading;
  const gateway = dialog.config.openRouter ?? {};
  const models = gateway.models ?? [];

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

  // Only ids that are usable as a launched model are offered as the default:
  // defaulting to a blank or malformed row would launch an id the guard drops.
  const selectable = selectableClaudeModelIds(models);

  return (
    <div className="grid gap-3">
      <div className="grid gap-2">
        <Label htmlFor="global-config-openrouter-baseurl">Gateway base URL</Label>
        <Input
          id="global-config-openrouter-baseurl"
          autoComplete="off"
          value={gateway.baseUrl ?? ''}
          disabled={disabled}
          placeholder="https://openrouter.ai/api"
          onChange={(event) => {
            patch({ baseUrl: event.target.value });
          }}
        />
        <div className="text-[12px] leading-[1.4] text-muted-foreground">
          The Anthropic-compatible endpoint every environment&apos;s Claude Code is routed through.
          Leave empty to keep environments on their own Claude sign-in.
        </div>
      </div>

      <div className="grid gap-2 sm:grid-cols-2">
        <div className="grid gap-2">
          <Label htmlFor="global-config-openrouter-secret">Credential Secret</Label>
          <Input
            id="global-config-openrouter-secret"
            autoComplete="off"
            value={gateway.authTokenSecret ?? ''}
            disabled={disabled}
            placeholder="erun-claude-gateway"
            onChange={(event) => {
              patch({ authTokenSecret: event.target.value });
            }}
          />
        </div>
        <div className="grid gap-2">
          <Label htmlFor="global-config-openrouter-secret-key">Secret key</Label>
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
        A Kubernetes Secret in each environment&apos;s namespace holding the gateway token. The
        value is never stored in config, and leaving the key empty uses {DEFAULT_TOKEN_KEY}.
      </div>

      <OpenRouterModelsField
        models={models}
        disabled={disabled}
        onAddModel={addModel}
        onSetModel={setModel}
        onRemoveModel={removeModel}
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

// OpenRouterModelsField edits the catalog rows themselves: the model ids an
// environment may select, and each id's context window.
function OpenRouterModelsField({
  models,
  disabled,
  onAddModel,
  onSetModel,
  onRemoveModel,
}: {
  models: UIOpenRouterModel[];
  disabled?: boolean;
  onAddModel: () => void;
  onSetModel: (index: number, values: Partial<UIOpenRouterModel>) => void;
  onRemoveModel: (index: number) => void;
}): React.ReactElement {
  return (
    <div className="grid gap-2">
      <div className="flex items-center justify-between gap-2">
        <Label>Models</Label>
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
      {models.length === 0 ? (
        <div className="text-[12px] leading-[1.4] text-muted-foreground">
          No models yet. Add the gateway model ids an environment may select.
        </div>
      ) : (
        models.map((model, index) => {
          const id = model.id.trim();
          const invalid = id !== '' && !isClaudeModelToken(id);
          return (
            <div
              key={index}
              data-openrouter-model={index}
              className="grid gap-2 sm:grid-cols-[1fr_10rem_auto] sm:items-start"
            >
              <div className="grid gap-1">
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
                  // An empty window is unknown rather than zero, so the launch
                  // leaves Claude Code's own assumption in place.
                  onSetModel(index, {
                    context: next === '' ? undefined : Math.trunc(Number(next)),
                  });
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
        })
      )}
      <div className="text-[12px] leading-[1.4] text-muted-foreground">
        A model&apos;s window must be the provider-level figure, which can be smaller than its
        advertised maximum. Leaving it empty makes Claude Code assume one.
      </div>
    </div>
  );
}
