import { Button, Input, Label } from 'erun-kit';
import * as React from 'react';

import { addClaudeModelId, isClaudeModelToken } from '@/components/app/claudeModels.helpers';
import type { UIEnvironmentConfig } from '@/types';

// ClaudeAddModelField types a model id the erun-level list does not offer. The
// catalog is the curated set, but a gateway serves ids the operator has not
// curated, and typing one is a supported act — refusing it here would leave no
// way to reach a model the gateway serves.
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
    // The base is what the user sees checked, so appending to it preserves any
    // catalog entries they have ticked rather than replacing the list.
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
