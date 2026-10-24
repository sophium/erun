import { Button, Input, Label, SelectField } from 'erun-kit';
import * as React from 'react';

import { addClaudeModelId, isClaudeModelToken } from '@/components/app/claudeModels.helpers';
import type { effectiveClaudeModelDefaults } from '@/components/app/ManageDialog.helpers';
import type { UIEnvironmentConfig } from '@/types';

// ClaudeAddModelField accepts ids the catalog does not list, since a gateway may serve them.
export function ClaudeAddModelField({
  value,
  known,
  defaults,
  overridden,
  disabled,
  onChange,
}: {
  value: string[];
  known: string[];
  defaults: UIEnvironmentConfig['claudeDefaults'];
  overridden: boolean;
  disabled?: boolean;
  onChange: (value: string[]) => void;
}): React.ReactElement {
  const [text, setText] = React.useState('');
  const trimmed = text.trim();
  const valid = isClaudeModelToken(trimmed);
  const duplicate = value.includes(trimmed);
  const canAdd = valid && !duplicate;

  const submit = () => {
    if (!canAdd) {
      return;
    }
    // Append to what is checked so ticked catalog entries are preserved.
    const base = overridden ? value : defaults.models;
    onChange(addClaudeModelId({ base, known, id: trimmed }));
    setText('');
  };

  return (
    <div className="grid gap-2">
      <Label htmlFor="environment-config-claude-add-model">Add a model id</Label>
      <div className="flex gap-2">
        <Input
          id="environment-config-claude-add-model"
          autoComplete="off"
          value={text}
          disabled={disabled}
          placeholder="e.g. deepseek/deepseek-v4.1-flash"
          aria-invalid={trimmed !== '' && !valid}
          onChange={(event) => {
            setText(event.target.value);
          }}
          onKeyDown={(event) => {
            if (event.key === 'Enter') {
              event.preventDefault();
              submit();
            }
          }}
        />
        <Button
          type="button"
          variant="secondary"
          size="sm"
          disabled={disabled === true || !canAdd}
          onClick={submit}
        >
          Add
        </Button>
      </div>
      <div className="text-[12px] leading-[1.4] text-muted-foreground">
        {trimmed === ''
          ? 'Add a gateway model id that the erun-level list does not offer.'
          : !valid
            ? 'A model id may contain only letters, digits, and . _ : / -'
            : duplicate
              ? 'That model is already selectable.'
              : `Adds ${trimmed} to this environment's available models.`}
      </div>
    </div>
  );
}

export function ClaudeDefaultModelField({
  claude,
  effective,
  disabled,
  onChange,
}: {
  claude: UIEnvironmentConfig['claude'];
  effective: ReturnType<typeof effectiveClaudeModelDefaults>;
  disabled?: boolean;
  onChange: (value: string | undefined) => void;
}): React.ReactElement {
  // Mirrors launch-side resolution; a stored model outside the set stays visible, flagged.
  const { available, defaultLaunchModel, viaGateway } = effective;
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
      label: viaGateway
        ? `${claude.defaultModel} (not in the gateway catalog)`
        : `${claude.defaultModel} (not in available models — ignored at launch)`,
    });
  }
  const helper = viaGateway
    ? "Model the AI tab preselects (claude --model), from the erun-level gateway catalog. Default launches the catalog's default model. Saving a change reopens open AI tabs; the Claude session resumes."
    : "Model the AI tab preselects (claude --model). Options are this environment's available models — tick a model above to make it selectable here. Default launches the first available model (never fable, which stays opt-in). Saving a change reopens open AI tabs; the Claude session resumes.";
  return (
    <SelectField
      id="environment-config-claude-default-model"
      label="Default model"
      value={claude.defaultModel ?? 'default'}
      options={options}
      helper={helper}
      disabled={disabled}
      onChange={(next) => {
        onChange(next === 'default' ? undefined : next);
      }}
    />
  );
}
