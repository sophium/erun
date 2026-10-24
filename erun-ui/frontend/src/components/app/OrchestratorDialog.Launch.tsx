import {
  Button,
  Checkbox,
  cn,
  FieldLabel,
  Input,
  Label,
  Select,
  SelectContent,
  SelectField,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from 'erun-kit';
import * as React from 'react';

import type { OrchestratorModel, OrchestratorUseGateway } from '@/app/slices/orchestratorsSlice';
import {
  ORCHESTRATOR_EFFORT_FIELD_ID,
  ORCHESTRATOR_GATEWAY_FIELD_ID,
  ORCHESTRATOR_MODEL_FIELD_ID,
  orchestratorEffortOptions,
  type OrchestratorGatewayControl,
  orchestratorGatewayControl,
  orchestratorGatewayModelHelper,
  orchestratorGatewayModelOptions,
  orchestratorGatewayModelUndriveable,
  type OrchestratorLaunchChoices,
  orchestratorLaunchSelectValue,
  orchestratorLaunchValue,
  orchestratorModelHelper,
  orchestratorModelInvalid,
} from '@/components/app/OrchestratorDialog.Launch.helpers';
import type { OrchestratorLaunchForm } from '@/components/app/OrchestratorDialog.Launch.hooks';

// OrchestratorLaunchFields: changes take effect on the session's next restart.
export function OrchestratorLaunchFields({
  launch,
  disabled,
}: {
  launch: OrchestratorLaunchForm;
  disabled?: boolean;
}): React.ReactElement {
  const { choices, choicesError, settings } = launch;
  const gateway = orchestratorGatewayControl(choices, settings.useGateway);
  return (
    <div className="grid gap-3">
      <OrchestratorGatewayField
        checked={settings.useGateway}
        control={gateway}
        choicesError={choicesError}
        choicesLoading={launch.choicesLoading}
        disabled={disabled}
        onChange={launch.setGateway}
        onRetry={launch.retryChoices}
      />
      {settings.useGateway ? (
        <OrchestratorGatewayModelField
          choices={choices}
          model={orchestratorLaunchValue(settings.model)}
          disabled={disabled}
          onChange={launch.setModel}
        />
      ) : (
        <OrchestratorModelInput
          choices={choices}
          model={settings.model}
          disabled={disabled}
          onChange={launch.setModel}
        />
      )}
      <SelectField
        id={ORCHESTRATOR_EFFORT_FIELD_ID}
        label="Effort"
        value={orchestratorLaunchSelectValue(settings.effort)}
        options={orchestratorEffortOptions(choices, settings.effort)}
        helper="How long Claude may think before responding. ultracode runs at xhigh effort and adds standing multi-agent orchestration."
        disabled={disabled}
        onChange={(next) => {
          launch.setEffort(orchestratorLaunchValue(next));
        }}
      />
    </div>
  );
}

// OrchestratorGatewayField ties its explanation to the checkbox, which also reports a blocked "on".
function OrchestratorGatewayField({
  checked,
  control,
  choicesError,
  choicesLoading,
  disabled,
  onChange,
  onRetry,
}: {
  checked: boolean;
  control: OrchestratorGatewayControl;
  choicesError: string;
  choicesLoading: boolean;
  disabled?: boolean;
  onChange: (useGateway: OrchestratorUseGateway) => void;
  onRetry: () => void;
}): React.ReactElement {
  const helpId = `${ORCHESTRATOR_GATEWAY_FIELD_ID}-help`;
  const problem = control.blocked || choicesError !== '';
  return (
    <div className="grid gap-2">
      <div className="flex items-center gap-2">
        <Checkbox
          id={ORCHESTRATOR_GATEWAY_FIELD_ID}
          checked={checked}
          disabled={disabled === true || control.disabled}
          aria-describedby={helpId}
          aria-invalid={control.blocked}
          onCheckedChange={(value) => {
            onChange(value === true);
          }}
        />
        <Label htmlFor={ORCHESTRATOR_GATEWAY_FIELD_ID} className="text-sm font-normal">
          Use gateway
        </Label>
      </div>
      <p
        id={helpId}
        className={cn(
          'text-[12px] leading-[1.4]',
          problem ? 'text-destructive' : 'text-muted-foreground',
        )}
      >
        {choicesError || control.helper}
      </p>
      {choicesError !== '' && (
        <div className="flex items-center gap-2">
          <span className="text-[12px] leading-[1.4] text-muted-foreground">
            Correct the gateway settings in ERun settings, then retry.
          </span>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={disabled === true || choicesLoading}
            onClick={onRetry}
          >
            {choicesLoading ? 'Reading the gateway catalog…' : 'Retry'}
          </Button>
        </div>
      )}
    </div>
  );
}

// OrchestratorGatewayModelField flags an undriveable stored model before save.
function OrchestratorGatewayModelField({
  choices,
  model,
  disabled,
  onChange,
}: {
  choices: OrchestratorLaunchChoices | null;
  model: OrchestratorModel;
  disabled?: boolean;
  onChange: (model: OrchestratorModel) => void;
}): React.ReactElement {
  const helpId = `${ORCHESTRATOR_MODEL_FIELD_ID}-help`;
  const invalid = orchestratorGatewayModelUndriveable(choices, model);
  return (
    <div className="grid min-w-0 gap-2">
      <FieldLabel htmlFor={ORCHESTRATOR_MODEL_FIELD_ID}>Model</FieldLabel>
      <Select
        value={orchestratorLaunchSelectValue(model)}
        disabled={disabled}
        onValueChange={(next) => {
          onChange(orchestratorLaunchValue(next));
        }}
      >
        <SelectTrigger
          id={ORCHESTRATOR_MODEL_FIELD_ID}
          className="w-full min-w-0 *:data-[slot=select-value]:min-w-0 *:data-[slot=select-value]:overflow-hidden *:data-[slot=select-value]:text-ellipsis"
          aria-describedby={helpId}
          aria-invalid={invalid}
        >
          <SelectValue />
        </SelectTrigger>
        <SelectContent position="popper" collisionPadding={12}>
          {orchestratorGatewayModelOptions(choices, model).map((item) => (
            <SelectItem key={item.value} value={item.value}>
              {item.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <p
        id={helpId}
        className={cn(
          'text-[12px] leading-[1.4]',
          invalid ? 'text-destructive' : 'text-muted-foreground',
        )}
      >
        {orchestratorGatewayModelHelper(choices, model)}
      </p>
    </div>
  );
}

// OrchestratorModelInput is free-text: off the gateway any model id is accepted.
function OrchestratorModelInput({
  choices,
  model,
  disabled,
  onChange,
}: {
  choices: OrchestratorLaunchChoices | null;
  model: OrchestratorModel;
  disabled?: boolean;
  onChange: (model: OrchestratorModel) => void;
}): React.ReactElement {
  const helpId = `${ORCHESTRATOR_MODEL_FIELD_ID}-help`;
  const invalid = orchestratorModelInvalid(model);
  const fallback = choices?.defaultModel ?? '';
  return (
    <div className="grid gap-2">
      <div className="flex items-center justify-between gap-2">
        <Label htmlFor={ORCHESTRATOR_MODEL_FIELD_ID}>Model</Label>
        {model !== '' && (
          <Button
            type="button"
            variant="link"
            size="sm"
            className="h-auto px-0 text-[12px]"
            disabled={disabled}
            onClick={() => {
              onChange('');
            }}
          >
            Reset to default
          </Button>
        )}
      </div>
      <Input
        id={ORCHESTRATOR_MODEL_FIELD_ID}
        autoComplete="off"
        spellCheck={false}
        value={model}
        placeholder={fallback ? `Default (${fallback})` : 'Default'}
        disabled={disabled}
        aria-describedby={helpId}
        aria-invalid={invalid}
        onChange={(event) => {
          onChange(event.target.value);
        }}
      />
      <div
        id={helpId}
        className={cn(
          'text-[12px] leading-[1.4]',
          invalid ? 'text-destructive' : 'text-muted-foreground',
        )}
      >
        {orchestratorModelHelper(choices, model)}
      </div>
    </div>
  );
}
