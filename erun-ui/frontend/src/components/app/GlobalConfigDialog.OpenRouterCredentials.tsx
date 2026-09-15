import { Button, Input, Label } from 'erun-kit';
import * as React from 'react';

import type { UIGatewayCredentialStatus } from '@/uiOpenRouterTypes';

// GatewayCredentialFields shows which gateway credential a deploy will deliver,
// and lets the operator replace it.
//
// There is no Secret to name and no entry within one to pick: the credential is
// one erun-level value, and erun delivers it into every environment's namespace
// itself. What is left for an operator to care about is which key is in play —
// so that is what this shows, identified by its last few characters rather than
// by its value.
export function GatewayCredentialFields({
  status,
  disabled,
  busy,
  onSave,
  onClear,
}: {
  status: UIGatewayCredentialStatus | undefined;
  disabled?: boolean;
  busy: boolean;
  onSave: (token: string) => void;
  onClear: () => void;
}): React.ReactElement {
  const [editing, setEditing] = React.useState(false);
  const [draft, setDraft] = React.useState('');
  const source = status?.source;

  if (editing) {
    return (
      <div className="grid gap-2">
        <Label htmlFor="global-config-openrouter-token">Gateway key</Label>
        <div className="flex items-center gap-2">
          <Input
            id="global-config-openrouter-token"
            autoComplete="off"
            type="password"
            value={draft}
            disabled={disabled}
            placeholder="sk-or-v1-..."
            onChange={(event) => {
              setDraft(event.target.value);
            }}
          />
          <Button
            type="button"
            size="sm"
            disabled={disabled === true || busy || draft.trim() === ''}
            onClick={() => {
              onSave(draft);
              setDraft('');
              setEditing(false);
            }}
          >
            Save key
          </Button>
          <Button
            type="button"
            size="sm"
            variant="ghost"
            disabled={busy}
            onClick={() => {
              setDraft('');
              setEditing(false);
            }}
          >
            Cancel
          </Button>
        </div>
        <div className="text-[12px] leading-[1.4] text-muted-foreground">
          {credentialHelperText()}
        </div>
      </div>
    );
  }

  return (
    <div className="grid gap-2">
      <Label>Gateway key</Label>
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm" data-gateway-credential-source={source ?? 'none'}>
          {credentialSummary(source, status?.hint)}
        </span>
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={disabled === true || busy}
          onClick={() => {
            setEditing(true);
          }}
        >
          {source === undefined ? 'Set a key' : 'Use a different key'}
        </Button>
        {/* Only offered when it would change something: with nothing saved, this
            machine's own key is already what a deploy delivers. */}
        {source === 'saved' ? (
          <Button type="button" variant="ghost" size="sm" disabled={busy} onClick={onClear}>
            Use this machine&apos;s key
          </Button>
        ) : null}
      </div>
      <div className="text-[12px] leading-[1.4] text-muted-foreground">
        {credentialHelperText()}
      </div>
    </div>
  );
}

// credentialSummary names the key in play without revealing it. The hint is a
// suffix, so two keys can be told apart and neither can be used.
function credentialSummary(source: string | undefined, hint: string | undefined): string {
  const suffix = hint === undefined || hint === '' ? '' : ` ${hint}`;
  if (source === 'saved') {
    return `Saved in ERun settings${suffix}`;
  }
  if (source === 'host') {
    return `This machine's Claude Code key${suffix}`;
  }
  return 'No gateway key found';
}

// credentialHelperText says what happens to the key, whichever field is shown:
// the delivery is the part an operator cannot see for themselves.
function credentialHelperText(): string {
  return 'ERun delivers this key into every environment that uses the gateway. Its value never enters a chart value, a saved config, or a launch command.';
}
