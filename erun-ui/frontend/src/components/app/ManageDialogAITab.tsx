import * as React from 'react';

import { useAppDispatch } from '@/app/hooks';
import { updateManageClaudeConfig } from '@/app/manageEnvironmentThunks';
import type { AppState } from '@/app/state';
import { CheckboxField } from '@/components/app/ManageDialog.fields';
import { isClaudeOverridden, isValidClaudeTokens } from '@/components/app/ManageDialog.helpers';
import { SelectField } from '@/components/app/SelectField';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { cn } from '@/lib/utils';
import type { UIEnvironmentConfig } from '@/types';

type ManageDialog = AppState['manageDialog'];

// Must list every per-env Claude override the AI tab edits, or "Reset all to
// defaults" leaves some behind.
const claudeOverrideResetValues: Partial<UIEnvironmentConfig['claude']> = {
  useMantle: undefined,
  useBedrock: undefined,
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
  const disabled = dialog.busy || dialog.configLoading;
  const overridden = isClaudeOverridden(claude);
  return (
    <div className="grid gap-3 rounded-[var(--radius)] border border-border p-3">
      <div className="flex items-center justify-between gap-3">
        <div className="text-xs leading-[1.2] font-semibold tracking-normal text-muted-foreground uppercase">
          Claude
        </div>
        {overridden && (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            disabled={disabled}
            onClick={() => {
              dispatch(updateManageClaudeConfig(claudeOverrideResetValues));
            }}
          >
            Reset all to defaults
          </Button>
        )}
      </div>
      <ClaudeBoolField
        id="environment-config-claude-mantle"
        label="Use Mantle"
        helper="When enabled, Claude requests for this environment are routed through Mantle. Default uses the global setting."
        defaultValue={defaults.useMantle}
        value={claude.useMantle}
        disabled={disabled}
        onChange={(useMantle) => {
          dispatch(updateManageClaudeConfig({ useMantle }));
        }}
      />
      <ClaudeBoolField
        id="environment-config-claude-bedrock"
        label="Use Bedrock"
        helper="When enabled, Claude requests are routed through AWS Bedrock. Mantle and Bedrock can both be enabled; the runtime decides which to use per request."
        defaultValue={defaults.useBedrock}
        value={claude.useBedrock}
        disabled={disabled}
        onChange={(useBedrock) => {
          dispatch(updateManageClaudeConfig({ useBedrock }));
        }}
      />
      <ClaudeModelsField
        defaults={defaults}
        value={claude.models ?? []}
        disabled={disabled}
        onChange={(models) => {
          dispatch(updateManageClaudeConfig({ models }));
        }}
      />
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
        defaults={defaults}
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
  // No global default to inherit, so off persists as absent (absent ≡ off)
  // rather than as a tri-state override like the sibling *bool fields.
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

function ClaudeDefaultModelField({
  claude,
  defaults,
  disabled,
  onChange,
}: {
  claude: UIEnvironmentConfig['claude'];
  defaults: UIEnvironmentConfig['claudeDefaults'];
  disabled?: boolean;
  onChange: (value: string | undefined) => void;
}): React.ReactElement {
  // The selectable models mirror what launch-side resolution honours, so an
  // operator can never pick a model the env does not expose. A stored model
  // no longer in that set stays visible as a flagged option but is dropped at
  // launch.
  const available = (claude.models?.length ?? 0) > 0 ? (claude.models ?? []) : defaults.models;
  // "Default" starts on the first available model rather than the agent's own
  // default, so name it "Default (<model>)" to show which model that is.
  const defaultLaunchModel = available[0] ?? '';
  const options = [
    {
      value: 'default',
      label: defaultLaunchModel ? `Default (${defaultLaunchModel})` : 'Default',
    },
    ...available.map((model) => ({ value: model, label: model })),
  ];
  if (claude.defaultModel && !available.includes(claude.defaultModel)) {
    options.push({
      value: claude.defaultModel,
      label: `${claude.defaultModel} (not in available models — ignored at launch)`,
    });
  }
  return (
    <SelectField
      id="environment-config-claude-default-model"
      label="Default model"
      value={claude.defaultModel ?? 'default'}
      options={options}
      helper="Model the AI tab preselects (claude --model). Options are this environment's available models — tick a model above to make it selectable here. Default launches the first available model (never fable, which stays opt-in). Saving a change reopens open AI tabs; the Claude session resumes."
      disabled={disabled}
      onChange={(next) => {
        onChange(next === 'default' ? undefined : next);
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

function ClaudeBoolField({
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
