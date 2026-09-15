import { Input, Label } from 'erun-kit';
import * as React from 'react';

import { ClaudeBoolField } from '@/components/app/ManageDialogAITab';
import type { UIEnvironmentConfig } from '@/types';

// ClaudeGatewayFields edits how this environment uses the erun-level gateway
// catalog. The catalog is one erun-level list rendered for every environment,
// so without these an environment could only be moved on or off the gateway by
// moving every environment with it.
//
// They render only when a catalog exists: a switch that changes nothing is
// worse than no switch, and the operator meets the catalog in ERun settings.
export function ClaudeGatewayFields({
  claude,
  disabled,
  onChange,
}: {
  claude: UIEnvironmentConfig['claude'];
  disabled?: boolean;
  onChange: (values: Partial<UIEnvironmentConfig['claude']>) => void;
}): React.ReactElement {
  const secret = claude.gatewayAuthTokenSecret ?? '';
  return (
    <div className="grid gap-3 rounded-[var(--radius)] border border-dashed border-border p-3">
      <ClaudeBoolField
        id="environment-config-claude-gateway"
        label="Use the gateway"
        helper="Whether this environment's Claude Code is routed through the erun-level gateway. Default follows the erun-level setting; off keeps this environment on its own Claude sign-in while every other environment still uses the gateway."
        defaultValue
        value={claude.useGateway}
        disabled={disabled}
        onChange={(useGateway) => {
          onChange({ useGateway });
        }}
      />
      <div className="grid gap-2">
        <Label htmlFor="environment-config-claude-gateway-secret">
          Gateway credential Secret (optional)
        </Label>
        <Input
          id="environment-config-claude-gateway-secret"
          autoComplete="off"
          value={secret}
          disabled={disabled}
          placeholder="Uses the erun-level catalog's Secret"
          onChange={(event) => {
            const next = event.target.value;
            // Absent rather than empty when cleared, so the environment goes
            // back to inheriting the catalog's name instead of naming "".
            onChange({ gatewayAuthTokenSecret: next === '' ? undefined : next });
          }}
        />
        <div className="text-[12px] leading-[1.4] text-muted-foreground">
          A Secret in this environment&apos;s namespace holding the gateway token, when it is
          managed separately from the catalog&apos;s own. Leave empty to use the catalog&apos;s.
        </div>
      </div>
    </div>
  );
}
