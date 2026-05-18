import * as React from 'react';

import { useAppDispatch } from '@/app/hooks';
import { updateManageClaudeConfig } from '@/app/manageEnvironmentThunks';
import type { AppState } from '@/app/state';
import { isClaudeOverridden, isValidClaudeTokens } from '@/components/app/ManageDialog.helpers';
import { SelectField } from '@/components/app/SelectField';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { cn } from '@/lib/utils';
import type { UIEnvironmentConfig } from '@/types';

type ManageDialog = AppState['manageDialog'];

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
              dispatch(
                updateManageClaudeConfig({
                  useMantle: undefined,
                  useBedrock: undefined,
                  models: [],
                  maxOutputTokens: undefined,
                }),
              );
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
    </div>
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
