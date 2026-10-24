import type { SelectFieldOption } from 'erun-kit';

import type {
  OrchestratorEffort,
  OrchestratorInfo,
  OrchestratorLaunchSettings,
  OrchestratorModel,
} from '@/app/slices/orchestratorsSlice';
import { isClaudeModelToken } from '@/components/app/claudeModels.helpers';

// OrchestratorLaunchChoices mirrors the Go orchestratorLaunchChoices read.
export interface OrchestratorLaunchChoices {
  gatewayConfigured: boolean;
  gatewayModels: string[];
  // Catalog entries the gateway cannot drive; never offered.
  gatewayUndriveableModels: string[];
  gatewayDefaultModel: string;
  defaultModel: string;
  effortLevels: string[];
  defaultEffort: string;
}

export const ORCHESTRATOR_GATEWAY_FIELD_ID = 'orchestrator-gateway';
export const ORCHESTRATOR_MODEL_FIELD_ID = 'orchestrator-model';
export const ORCHESTRATOR_EFFORT_FIELD_ID = 'orchestrator-effort';

// ORCHESTRATOR_LAUNCH_DEFAULT stands in for '' (Radix rejects empty values); it is also the CLI's clearing word.
export const ORCHESTRATOR_LAUNCH_DEFAULT = 'default';

// orchestratorLaunchValue maps the clearing word, in any case, to '' as the CLI does.
export function orchestratorLaunchValue(raw: string): string {
  return raw.trim().toLowerCase() === ORCHESTRATOR_LAUNCH_DEFAULT ? '' : raw;
}

export function orchestratorLaunchSettingsToSave(
  settings: OrchestratorLaunchSettings,
): OrchestratorLaunchSettings {
  return {
    useGateway: settings.useGateway,
    model: orchestratorLaunchValue(settings.model),
    effort: orchestratorLaunchValue(settings.effort),
  };
}

export function orchestratorLaunchSelectValue(value: string): string {
  return value === '' ? ORCHESTRATOR_LAUNCH_DEFAULT : value;
}

// orchestratorLaunchSettingsOf seeds from stored settings, not the resolved launch, so defaults stay defaults.
export function orchestratorLaunchSettingsOf(
  editing: OrchestratorInfo | null,
): OrchestratorLaunchSettings {
  return {
    useGateway: editing?.useGateway ?? false,
    model: editing?.model ?? '',
    effort: editing?.effort ?? '',
  };
}

export interface OrchestratorGatewayControl {
  disabled: boolean;
  helper: string;
  // A stored "on" that cannot launch.
  blocked: boolean;
}

// orchestratorGatewayControl disables the switch without a catalog, unless it is already on.
export function orchestratorGatewayControl(
  choices: OrchestratorLaunchChoices | null,
  useGateway: boolean,
): OrchestratorGatewayControl {
  if (!choices) {
    return { disabled: !useGateway, helper: 'Reading the gateway catalog…', blocked: false };
  }
  if (!choices.gatewayConfigured) {
    return useGateway
      ? {
          disabled: false,
          helper:
            'No gateway catalog is configured, so this orchestrator cannot start through it. Configure the gateway in ERun settings, or turn this off.',
          blocked: true,
        }
      : {
          disabled: true,
          helper:
            'No gateway catalog is configured. Configure the gateway in ERun settings to route this orchestrator through it.',
          blocked: false,
        };
  }
  return {
    disabled: false,
    helper: useGateway
      ? "Routes this orchestrator's Claude Code through the gateway configured in ERun settings, authenticated with the gateway key saved there."
      : "Off — runs on this machine's own Claude Code sign-in and settings.",
    blocked: false,
  };
}

export function orchestratorGatewayModelUndriveable(
  choices: OrchestratorLaunchChoices | null,
  model: OrchestratorModel,
): boolean {
  return model !== '' && (choices?.gatewayUndriveableModels ?? []).includes(model);
}

// orchestratorGatewayModelOptions keeps a stored unlisted model visible, flagged.
export function orchestratorGatewayModelOptions(
  choices: OrchestratorLaunchChoices | null,
  model: OrchestratorModel,
): SelectFieldOption[] {
  const models = choices?.gatewayModels ?? [];
  // No driveable model means the orchestrator's own default applies.
  const resolved = choices
    ? choices.gatewayDefaultModel !== ''
      ? choices.gatewayDefaultModel
      : choices.defaultModel
    : '';
  const options: SelectFieldOption[] = [
    {
      value: ORCHESTRATOR_LAUNCH_DEFAULT,
      label: resolved ? `Default (${resolved})` : 'Default',
    },
    ...models.map((id) => ({ value: id, label: id })),
  ];
  if (model !== '' && !models.includes(model)) {
    // Flag only once the catalog is read, to avoid a flash while in flight.
    options.push({ value: model, label: orchestratorUnofferedModelLabel(choices, model) });
  }
  return options;
}

function orchestratorUnofferedModelLabel(
  choices: OrchestratorLaunchChoices | null,
  model: OrchestratorModel,
): string {
  if (!choices) {
    return model;
  }
  return orchestratorGatewayModelUndriveable(choices, model)
    ? `${model} (cannot be driven through the gateway)`
    : `${model} (not in the gateway catalog)`;
}

export function orchestratorGatewayModelHelper(
  choices: OrchestratorLaunchChoices | null,
  model: OrchestratorModel,
): string {
  if (orchestratorGatewayModelUndriveable(choices, model)) {
    return `The gateway cannot drive ${model}, so this orchestrator would not start. Choose another model or Default.`;
  }
  return "The gateway catalog's models. Default launches on the catalog's default model.";
}

// orchestratorEffortOptions keeps an unknown stored level visible, flagged.
export function orchestratorEffortOptions(
  choices: OrchestratorLaunchChoices | null,
  effort: OrchestratorEffort,
): SelectFieldOption[] {
  const levels = choices?.effortLevels ?? [];
  const options: SelectFieldOption[] = [
    {
      value: ORCHESTRATOR_LAUNCH_DEFAULT,
      label: choices?.defaultEffort ? `Default (${choices.defaultEffort})` : 'Default',
    },
    ...levels.map((level) => ({ value: level, label: level })),
  ];
  if (effort !== '' && !levels.includes(effort)) {
    options.push({
      value: effort,
      label: choices ? `${effort} (not a known effort level)` : effort,
    });
  }
  return options;
}

// orchestratorModelInvalid mirrors the Go launch guard: one argv token, not starting with -.
export function orchestratorModelInvalid(model: OrchestratorModel): boolean {
  return model.trim() !== '' && !isClaudeModelToken(model);
}

export function orchestratorModelHelper(
  choices: OrchestratorLaunchChoices | null,
  model: OrchestratorModel,
): string {
  if (orchestratorModelInvalid(model)) {
    return 'A model id may contain only letters, digits, and . _ : / - and cannot start with -.';
  }
  const fallback = choices?.defaultModel ?? '';
  if (orchestratorLaunchValue(model).trim() === '') {
    return fallback
      ? `Using the default (${fallback}). Type a model id to launch on another.`
      : 'Using the default model. Type a model id to launch on another.';
  }
  return `Launches on ${model.trim()} (claude --model).`;
}
