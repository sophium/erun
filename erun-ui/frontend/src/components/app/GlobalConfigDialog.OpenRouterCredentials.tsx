import { Button, Input, Label } from 'erun-kit';
import * as React from 'react';

import type { UIGatewayCredentialStatus } from '@/uiOpenRouterTypes';

// GatewayCredentialFields identifies the key in play by its suffix, never its value.
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
          {credentialHelperText(status)}
        </div>
      </div>
    );
  }

  return (
    <div className="grid gap-2">
      <Label>Gateway key</Label>
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm" data-gateway-credential-source={source ?? 'none'}>
          {credentialSummary(status)}
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
        {/* With nothing saved, the machine's own key is already delivered. */}
        {source === 'saved' ? (
          <Button type="button" variant="ghost" size="sm" disabled={busy} onClick={onClear}>
            Use this machine&apos;s key
          </Button>
        ) : null}
      </div>
      <div className="text-[12px] leading-[1.4] text-muted-foreground">
        {credentialHelperText(status)}
      </div>
    </div>
  );
}

// A key belonging to another gateway is reported as such, not as missing.
function credentialSummary(status: UIGatewayCredentialStatus | undefined): string {
  const suffix = status?.hint ? ` ${status.hint}` : '';
  if (status?.source === 'saved') {
    return `Saved in ERun settings${suffix}`;
  }
  if (status?.source === 'host') {
    return `This machine's Claude Code key${suffix}`;
  }
  if (status?.hostEndpoint) {
    return 'No key for this gateway';
  }
  return 'No gateway key found';
}

function credentialHelperText(status: UIGatewayCredentialStatus | undefined): string {
  const delivery =
    'ERun delivers this key into every environment that uses the gateway. Its value never enters a chart value, a saved config, or a launch command.';
  if (status?.source === undefined && status?.hostEndpoint) {
    return `This machine's Claude Code key belongs to ${status.hostEndpoint}, so it is not reused here — a key is only ever sent to the gateway it was issued for. Save this gateway's own key. ${delivery}`;
  }
  return delivery;
}
