import { Button, Input, Label } from 'erun-kit';
import * as React from 'react';

import { isClaudeModelToken } from '@/components/app/claudeModels.helpers';
import type { effectiveClaudeModelDefaults } from '@/components/app/ManageDialog.helpers';
import { ClaudeBoolField } from '@/components/app/ManageDialogAITab';
import type { UIEnvironmentConfig } from '@/types';

// ClaudeGatewayFields edits this env's use of the erun-level gateway catalog; it renders only when one exists.
// The credential is erun-level and delivered by deploy, so there is no per-env credential field.
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

// ClaudeGatewayModelsField replaces the env's own model list while the gateway applies; a typed id becomes the default.
export function ClaudeGatewayModelsField({
  effective,
  disabled,
  onDefaultModel,
}: {
  effective: ReturnType<typeof effectiveClaudeModelDefaults>;
  disabled?: boolean;
  onDefaultModel: (model: string) => void;
}): React.ReactElement {
  const [text, setText] = React.useState('');
  const trimmed = text.trim();
  const valid = isClaudeModelToken(trimmed);
  const submit = () => {
    if (!valid) {
      return;
    }
    onDefaultModel(trimmed);
    setText('');
  };
  return (
    <div className="grid gap-2">
      <Label htmlFor="environment-config-claude-gateway-model">Gateway models</Label>
      {effective.available.length === 0 ? (
        <p
          className="text-xs text-amber-700 dark:text-amber-400 [overflow-wrap:anywhere]"
          data-testid="environment-config-claude-gateway-no-models"
        >
          This environment uses the gateway, but its catalog lists no model an AI tab can run. Add a
          runnable model to the gateway catalog in ERun settings, type a model id below, or turn off
          &quot;Use the gateway&quot;.
        </p>
      ) : (
        <p className="text-[12px] leading-[1.4] text-muted-foreground">
          From the erun-level gateway catalog (ERun settings): {effective.available.join(', ')}.
        </p>
      )}
      <div className="flex gap-2">
        <Input
          id="environment-config-claude-gateway-model"
          autoComplete="off"
          value={text}
          disabled={disabled}
          placeholder="A model id the catalog does not list"
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
          disabled={disabled === true || !valid}
          onClick={submit}
        >
          Use
        </Button>
      </div>
    </div>
  );
}

// ClaudeProviderFields are the env's Mantle and Bedrock choices, overridden while a gateway applies.
export function ClaudeProviderFields({
  claude,
  defaults,
  viaGateway,
  disabled,
  onChange,
}: {
  claude: UIEnvironmentConfig['claude'];
  defaults: UIEnvironmentConfig['claudeDefaults'];
  viaGateway: boolean;
  disabled?: boolean;
  onChange: (values: Partial<UIEnvironmentConfig['claude']>) => void;
}): React.ReactElement {
  return (
    <>
      <ClaudeBoolField
        id="environment-config-claude-mantle"
        label="Use Mantle"
        helper={providerHelper(
          'When enabled, Claude requests for this environment are routed through Mantle. Default uses the global setting.',
          viaGateway,
        )}
        defaultValue={defaults.useMantle}
        value={claude.useMantle}
        disabled={disabled}
        onChange={(useMantle) => {
          onChange({ useMantle });
        }}
      />
      <ClaudeBoolField
        id="environment-config-claude-bedrock"
        label="Use Bedrock"
        helper={providerHelper(
          'When enabled, Claude requests are routed through AWS Bedrock. Mantle and Bedrock can both be enabled; the runtime decides which to use per request.',
          viaGateway,
        )}
        defaultValue={defaults.useBedrock}
        value={claude.useBedrock}
        disabled={disabled}
        onChange={(useBedrock) => {
          onChange({ useBedrock });
        }}
      />
    </>
  );
}

// The gateway replaces the model provider, so deploy renders Mantle and Bedrock off.
function providerHelper(helper: string, viaGateway: boolean): string {
  return viaGateway
    ? `${helper} Ignored while this environment uses the gateway, which replaces the model provider.`
    : helper;
}
