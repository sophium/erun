import * as React from 'react';

import { ClaudeBoolField } from '@/components/app/ManageDialogAITab';
import type { UIEnvironmentConfig } from '@/types';

// ClaudeGatewayFields edits how this environment uses the erun-level gateway
// catalog. The catalog is one erun-level list rendered for every environment,
// so without this an environment could only be moved on or off the gateway by
// moving every environment with it.
//
// There is deliberately no credential field here. The credential is one
// erun-level value in the operator's secret store, delivered into this
// environment's namespace by deploy — so a per-environment reference would be a
// second way to say the same thing, and the wrong one when the catalog is
// global.
//
// It renders only when a catalog exists: a switch that changes nothing is worse
// than no switch, and the operator meets the catalog in ERun settings.
export function ClaudeGatewayFields({
  claude,
  disabled,
  onChange,
}: {
  claude: UIEnvironmentConfig['claude'];
  disabled?: boolean;
  onChange: (values: Partial<UIEnvironmentConfig['claude']>) => void;
}): React.ReactElement {
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
    </div>
  );
}
