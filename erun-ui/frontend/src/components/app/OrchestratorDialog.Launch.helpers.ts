import type { SelectFieldOption } from 'erun-kit';

import type {
  OrchestratorEffort,
  OrchestratorInfo,
  OrchestratorLaunchSettings,
  OrchestratorModel,
} from '@/app/slices/orchestratorsSlice';
import { isClaudeModelToken } from '@/components/app/claudeModels.helpers';

// OrchestratorLaunchChoices mirrors the Go orchestratorLaunchChoices read:
// what the gateway, model and effort controls can offer on this machine.
export interface OrchestratorLaunchChoices {
  gatewayConfigured: boolean;
  gatewayModels: string[];
  // Catalog entries the gateway cannot drive; never offered, and flagged when
  // a stored model names one.
  gatewayUndriveableModels: string[];
  gatewayDefaultModel: string;
  defaultModel: string;
  effortLevels: string[];
  defaultEffort: string;
}

export const ORCHESTRATOR_GATEWAY_FIELD_ID = 'orchestrator-gateway';
export const ORCHESTRATOR_MODEL_FIELD_ID = 'orchestrator-model';
export const ORCHESTRATOR_EFFORT_FIELD_ID = 'orchestrator-effort';

// ORCHESTRATOR_LAUNCH_DEFAULT is the pickers' sentinel for an empty setting.
// Radix's Select.Item rejects an empty-string value, so '' travels as this
// token and is translated back by orchestratorLaunchValue. It is the same word
// `erun orchestrator set-model/set-effort` take to clear a setting, and the
// free-entry model field can still have it typed into it, so every value is
// passed through orchestratorLaunchValue before it is shown as a choice or
// sent.
export const ORCHESTRATOR_LAUNCH_DEFAULT = 'default';

// orchestratorLaunchValue maps the clearing word, in any case and with
// surrounding blanks, to the empty default, as the CLI does.
export function orchestratorLaunchValue(raw: string): string {
  return raw.trim().toLowerCase() === ORCHESTRATOR_LAUNCH_DEFAULT ? '' : raw;
}

// orchestratorLaunchSettingsToSave is what the dialog sends: the clearing word
// typed as a model or effort never leaves the form as a literal value.
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

// orchestratorLaunchSettingsOf seeds the form from the stored settings, never
// from the resolved launch, so the defaults stay defaults on save.
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
  // blocked marks a stored "on" that cannot launch, so it reads as a problem
  // to resolve rather than as an ordinary description.
  blocked: boolean;
}

// orchestratorGatewayControl decides whether the gateway switch can be used and
// what it says. With no catalog the switch is disabled, except while it is
// already on: that value cannot launch, and turning it off is the remedy.
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

// orchestratorGatewayModelUndriveable reports a stored model the catalog names
// as one the gateway cannot drive: a launch on it would be refused.
export function orchestratorGatewayModelUndriveable(
  choices: OrchestratorLaunchChoices | null,
  model: OrchestratorModel,
): boolean {
  return model !== '' && (choices?.gatewayUndriveableModels ?? []).includes(model);
}

// orchestratorGatewayModelOptions lists the gateway catalog's models plus the
// default, naming which model the default resolves to. A stored model the
// catalog does not offer stays visible, flagged with why, so the trigger never
// renders blank over a value the next save sends back.
export function orchestratorGatewayModelOptions(
  choices: OrchestratorLaunchChoices | null,
  model: OrchestratorModel,
): SelectFieldOption[] {
  const models = choices?.gatewayModels ?? [];
  // A catalog listing no driveable model supplies no default, so the
  // orchestrator's own default passes through it.
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
    // Flagged only once the catalog has been read, so a stored model never
    // flashes as unlisted while the read is in flight.
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

// orchestratorGatewayModelHelper explains the gateway model picker, and says
// what to do when the stored model cannot launch through it.
export function orchestratorGatewayModelHelper(
  choices: OrchestratorLaunchChoices | null,
  model: OrchestratorModel,
): string {
  if (orchestratorGatewayModelUndriveable(choices, model)) {
    return `The gateway cannot drive ${model}, so this orchestrator would not start. Choose another model or Default.`;
  }
  return "The gateway catalog's models. Default launches on the catalog's default model.";
}

// orchestratorEffortOptions lists the effort levels plus the default. A stored
// level this build does not know stays visible, flagged, for the same reason.
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

// orchestratorModelInvalid mirrors the Go launch guard: a model reaches
// `--model` as one argv token, so it is letters, digits and . _ : / - and
// cannot start with -.
export function orchestratorModelInvalid(model: OrchestratorModel): boolean {
  return model.trim() !== '' && !isClaudeModelToken(model);
}

// orchestratorModelHelper explains the free-entry model field used off the
// gateway, where any model this machine's Claude Code accepts can be named.
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
