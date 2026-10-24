import { Button, Checkbox, cn, Input, Label, SelectField } from 'erun-kit';
import * as React from 'react';

import { useAppDispatch } from '@/app/hooks';
import { updateManageClaudeConfig } from '@/app/manageEnvironmentThunks';
import type { AppState } from '@/app/state';
import { CheckboxField } from '@/components/app/ManageDialog.fields';
import {
  effectiveClaudeModelDefaults,
  isClaudeOverridden,
  isValidClaudeTokens,
} from '@/components/app/ManageDialog.helpers';
import {
  ClaudeGatewayFields,
  ClaudeGatewayModelsField,
  ClaudeProviderFields,
} from '@/components/app/ManageDialogAIClaudeGateway';
import {
  ClaudeAddModelField,
  ClaudeDefaultModelField,
} from '@/components/app/ManageDialogAIClaudeModels';
import type { UIEnvironmentConfig } from '@/types';

type ManageDialog = AppState['manageDialog'];

// Must list every per-env Claude override, or "Reset all to defaults" misses some.
const claudeOverrideResetValues: Partial<UIEnvironmentConfig['claude']> = {
  useMantle: undefined,
  useBedrock: undefined,
  useGateway: undefined,
  models: [],
  maxOutputTokens: undefined,
  effort: undefined,
  defaultModel: undefined,
  verboseDebug: undefined,
};

export function ClaudeSettingsSection({ dialog }: { dialog: ManageDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  const config = dialog.config;
  const claude = config.claude;
  const defaults = config.claudeDefaults;
  const effective = effectiveClaudeModelDefaults(claude, defaults);
  const disabled = dialog.busy || dialog.configLoading;
  const overridden = isClaudeOverridden(claude);
  return (
    <div className="grid gap-3 rounded-[var(--radius)] border border-border p-3">
      <ClaudeSectionHeader
        overridden={overridden}
        disabled={disabled}
        onReset={() => {
          dispatch(updateManageClaudeConfig(claudeOverrideResetValues));
        }}
      />
      <ClaudeProviderFields
        claude={claude}
        defaults={defaults}
        viaGateway={effective.viaGateway}
        disabled={disabled}
        onChange={(values) => {
          dispatch(updateManageClaudeConfig(values));
        }}
      />
      {effective.viaGateway ? (
        <ClaudeGatewayModelsField
          effective={effective}
          disabled={disabled}
          onDefaultModel={(defaultModel) => {
            dispatch(updateManageClaudeConfig({ defaultModel }));
          }}
        />
      ) : (
        <ClaudeModelsField
          defaults={defaults}
          value={claude.models ?? []}
          disabled={disabled}
          onChange={(models) => {
            dispatch(updateManageClaudeConfig({ models }));
          }}
        />
      )}
      <ClaudeMaxTokensField
        defaults={defaults}
        value={claude.maxOutputTokens}
        disabled={disabled}
        onChange={(maxOutputTokens) => {
          dispatch(updateManageClaudeConfig({ maxOutputTokens }));
        }}
      />
      <ClaudeEffortField
        defaults={defaults}
        value={claude.effort}
        disabled={disabled}
        onChange={(effort) => {
          dispatch(updateManageClaudeConfig({ effort }));
        }}
      />
      <ClaudeDefaultModelField
        claude={claude}
        effective={effective}
        disabled={disabled}
        onChange={(defaultModel) => {
          dispatch(updateManageClaudeConfig({ defaultModel }));
        }}
      />
      <ClaudeVerboseDebugField
        claude={claude}
        disabled={disabled}
        onChange={(verboseDebug) => {
          dispatch(updateManageClaudeConfig({ verboseDebug }));
        }}
      />
      {defaults.gatewayConfigured ? (
        <ClaudeGatewayFields
          claude={claude}
          disabled={disabled}
          onChange={(values) => {
            dispatch(updateManageClaudeConfig(values));
          }}
        />
      ) : null}
    </div>
  );
}

function ClaudeSectionHeader({
  overridden,
  disabled,
  onReset,
}: {
  overridden: boolean;
  disabled: boolean;
  onReset: () => void;
}): React.ReactElement {
  return (
    <div className="flex items-center justify-between gap-3">
      <div className="text-xs leading-[1.2] font-semibold tracking-normal text-muted-foreground uppercase">
        Claude
      </div>
      {overridden && (
        <Button type="button" variant="ghost" size="sm" disabled={disabled} onClick={onReset}>
          Reset all to defaults
        </Button>
      )}
    </div>
  );
}

function ClaudeVerboseDebugField({
  claude,
  disabled,
  onChange,
}: {
  claude: UIEnvironmentConfig['claude'];
  disabled?: boolean;
  onChange: (value: true | undefined) => void;
}): React.ReactElement {
  // No global default to inherit, so off persists as absent.
  return (
    <CheckboxField
      id="environment-config-claude-verbose-debug"
      label="Launch Claude in verbose + debug mode"
      helper="Adds --verbose --debug when an AI tab launches Claude (env and contribute AI tabs). Saving a change reopens open AI tabs to apply it; the Claude session resumes. Does not affect the deployed runtime."
      checked={claude.verboseDebug === true}
      disabled={disabled}
      onChange={(checked) => {
        onChange(checked ? true : undefined);
      }}
    />
  );
}

function ClaudeEffortField({
  defaults,
  value,
  disabled,
  onChange,
}: {
  defaults: UIEnvironmentConfig['claudeDefaults'];
  value: string | undefined;
  disabled?: boolean;
  onChange: (value: string | undefined) => void;
}): React.ReactElement {
  return (
    <SelectField
      id="environment-config-claude-effort"
      label="Effort"
      value={value ?? 'default'}
      options={[
        { value: 'default', label: `Default (${defaults.effort})` },
        ...defaults.effortLevels.map((level) => ({ value: level, label: level })),
      ]}
      helper="Effort level Claude runs at in this environment's AI tab. Higher levels let Claude think longer before responding. ultracode runs at xhigh effort and additionally enables standing multi-agent workflow orchestration; it is the default."
      disabled={disabled}
      onChange={(next) => {
        onChange(next === 'default' ? undefined : next);
      }}
    />
  );
}

export function ClaudeBoolField({
  id,
  label,
  defaultValue,
  value,
  helper,
  disabled,
  onChange,
}: {
  id: string;
  label: string;
  defaultValue: boolean;
  value: boolean | undefined;
  helper?: string;
  disabled?: boolean;
  onChange: (value: boolean | undefined) => void;
}): React.ReactElement {
  const selectValue = value === undefined ? 'default' : value ? 'on' : 'off';
  const defaultLabel = defaultValue ? 'Default (enabled)' : 'Default (disabled)';
  return (
    <SelectField
      id={id}
      label={label}
      value={selectValue}
      options={[
        { value: 'default', label: defaultLabel },
        { value: 'on', label: 'Enabled' },
        { value: 'off', label: 'Disabled' },
      ]}
      helper={helper}
      disabled={disabled}
      onChange={(next) => {
        if (next === 'default') {
          onChange(undefined);
        } else {
          onChange(next === 'on');
        }
      }}
    />
  );
}

function ClaudeModelsField({
  defaults,
  value,
  disabled,
  onChange,
}: {
  defaults: UIEnvironmentConfig['claudeDefaults'];
  value: string[];
  disabled?: boolean;
  onChange: (value: string[]) => void;
}): React.ReactElement {
  const overridden = value.length > 0;
  const known = defaults.knownModels.length > 0 ? defaults.knownModels : defaults.models;
  const displayValue = new Set(overridden ? value : defaults.models);
  const baseId = 'environment-config-claude-models';
  const helpId = `${baseId}-help`;
  return (
    <div className="grid gap-2">
      <div className="flex items-center justify-between gap-2">
        <Label htmlFor={baseId}>Available models</Label>
        {overridden && (
          <Button
            type="button"
            variant="link"
            size="sm"
            className="h-auto px-0 text-[12px]"
            disabled={disabled}
            onClick={() => {
              onChange([]);
            }}
          >
            Reset to default
          </Button>
        )}
      </div>
      <div
        id={baseId}
        role="group"
        aria-describedby={helpId}
        className="flex flex-wrap gap-x-4 gap-y-2"
      >
        {known.map((model) => (
          <ClaudeModelCheckbox
            key={model}
            baseId={baseId}
            model={model}
            known={known}
            value={value}
            defaults={defaults}
            overridden={overridden}
            displayValue={displayValue}
            disabled={disabled}
            onChange={onChange}
          />
        ))}
      </div>
      <div id={helpId} className="text-[12px] leading-[1.4] text-muted-foreground">
        {overridden
          ? `Overridden. Default: ${defaults.models.join(', ') || 'none'}.`
          : `Using default (${defaults.models.join(', ') || 'none'}).`}
      </div>
      <ClaudeAddModelField
        value={value}
        known={known}
        defaults={defaults}
        overridden={overridden}
        disabled={disabled}
        onChange={onChange}
      />
    </div>
  );
}

function ClaudeModelCheckbox({
  baseId,
  model,
  known,
  value,
  defaults,
  overridden,
  displayValue,
  disabled,
  onChange,
}: {
  baseId: string;
  model: string;
  known: string[];
  value: string[];
  defaults: UIEnvironmentConfig['claudeDefaults'];
  overridden: boolean;
  displayValue: Set<string>;
  disabled?: boolean;
  onChange: (value: string[]) => void;
}): React.ReactElement {
  const checkboxId = `${baseId}-${model}`;
  const checked = displayValue.has(model);
  return (
    <label
      htmlFor={checkboxId}
      className={cn(
        'flex items-center gap-2 text-sm',
        overridden ? 'text-foreground' : 'text-muted-foreground',
      )}
    >
      <Checkbox
        id={checkboxId}
        checked={checked}
        disabled={disabled}
        onCheckedChange={(next) => {
          const base = overridden ? value : defaults.models;
          const set = new Set(base);
          if (next) {
            set.add(model);
          } else {
            set.delete(model);
          }
          const ordered = known.filter((entry) => set.has(entry));
          for (const entry of base) {
            if (!known.includes(entry) && set.has(entry)) {
              ordered.push(entry);
            }
          }
          onChange(ordered);
        }}
      />
      {model}
    </label>
  );
}

function ClaudeMaxTokensField({
  defaults,
  value,
  disabled,
  onChange,
}: {
  defaults: UIEnvironmentConfig['claudeDefaults'];
  value: number | undefined;
  disabled?: boolean;
  onChange: (value: number | undefined) => void;
}): React.ReactElement {
  const id = 'environment-config-claude-maxtokens';
  const helpId = `${id}-help`;
  const overridden = value !== undefined;
  const [text, setText] = React.useState<string>(overridden ? String(value) : '');
  React.useEffect(() => {
    setText(overridden ? String(value) : '');
  }, [value, overridden]);
  const invalid = text.trim() !== '' && !isValidClaudeTokens(text, defaults);
  return (
    <div className="grid gap-2">
      <div className="flex items-center justify-between gap-2">
        <Label htmlFor={id}>Max output tokens</Label>
        {overridden && (
          <Button
            type="button"
            variant="link"
            size="sm"
            className="h-auto px-0 text-[12px]"
            disabled={disabled}
            onClick={() => {
              onChange(undefined);
            }}
          >
            Reset to default
          </Button>
        )}
      </div>
      <Input
        id={id}
        type="number"
        inputMode="numeric"
        min={defaults.minTokens}
        max={defaults.maxTokens}
        step={1}
        autoComplete="off"
        value={text}
        placeholder={`Default: ${String(defaults.maxOutputTokens)}`}
        disabled={disabled}
        aria-describedby={helpId}
        aria-invalid={invalid}
        onChange={(event) => {
          const next = event.target.value;
          setText(next);
          if (next.trim() === '') {
            onChange(undefined);
            return;
          }
          if (!isValidClaudeTokens(next, defaults)) {
            return;
          }
          onChange(Math.trunc(Number(next)));
        }}
      />
      <div
        id={helpId}
        className={cn(
          'text-[12px] leading-[1.4]',
          invalid ? 'text-destructive' : 'text-muted-foreground',
        )}
      >
        {invalid
          ? `Enter an integer between ${String(defaults.minTokens)} and ${String(defaults.maxTokens)}.`
          : overridden
            ? `Overridden. Default: ${String(defaults.maxOutputTokens)}.`
            : `Using default (${String(defaults.maxOutputTokens)}).`}
      </div>
    </div>
  );
}
