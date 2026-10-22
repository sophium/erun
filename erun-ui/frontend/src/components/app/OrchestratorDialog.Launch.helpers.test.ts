import assert from 'node:assert/strict';

import { test } from 'vitest';

import type { OrchestratorInfo } from '@/app/slices/orchestratorsSlice';

import {
  ORCHESTRATOR_LAUNCH_DEFAULT,
  orchestratorEffortOptions,
  orchestratorGatewayControl,
  orchestratorGatewayModelHelper,
  orchestratorGatewayModelOptions,
  orchestratorGatewayModelUndriveable,
  type OrchestratorLaunchChoices,
  orchestratorLaunchSelectValue,
  orchestratorLaunchSettingsOf,
  orchestratorLaunchSettingsToSave,
  orchestratorLaunchValue,
  orchestratorModelHelper,
  orchestratorModelInvalid,
} from './OrchestratorDialog.Launch.helpers';

const LEVELS = ['low', 'medium', 'high', 'xhigh', 'max', 'ultracode'];

function choices(overrides: Partial<OrchestratorLaunchChoices> = {}): OrchestratorLaunchChoices {
  return {
    gatewayConfigured: true,
    gatewayModels: ['deepseek/deepseek-v4.1-flash', 'anthropic/claude-fable-5.1'],
    gatewayUndriveableModels: ['acme/reasoner'],
    gatewayDefaultModel: 'deepseek/deepseek-v4.1-flash',
    defaultModel: 'opus',
    effortLevels: LEVELS,
    defaultEffort: 'ultracode',
    ...overrides,
  };
}

function values(options: { value: string }[]): string[] {
  return options.map((option) => option.value);
}

// The form seeds from what is stored, so an orchestrator with no settings
// sends defaults back on save rather than the values they resolve to.
test('seeds the form from the stored settings, defaulting absent ones', () => {
  assert.deepEqual(orchestratorLaunchSettingsOf(null), {
    useGateway: false,
    model: '',
    effort: '',
  });
  assert.deepEqual(
    orchestratorLaunchSettingsOf({
      useGateway: true,
      model: 'm',
      effort: 'high',
    } as OrchestratorInfo),
    { useGateway: true, model: 'm', effort: 'high' },
  );
});

test('round-trips the default sentinel to the empty field value', () => {
  assert.equal(orchestratorLaunchValue(ORCHESTRATOR_LAUNCH_DEFAULT), '');
  assert.equal(orchestratorLaunchValue('high'), 'high');
  assert.equal(orchestratorLaunchSelectValue(''), ORCHESTRATOR_LAUNCH_DEFAULT);
  assert.equal(orchestratorLaunchSelectValue('high'), 'high');
});

// The CLI clears a setting with `--model default` in any case; the free-entry
// field accepts the same word, and it must clear rather than be stored.
test('the clearing word typed in any case is sent as the default', () => {
  for (const typed of ['default', 'Default', 'DEFAULT', '  default ']) {
    assert.equal(orchestratorLaunchValue(typed), '');
    assert.deepEqual(
      orchestratorLaunchSettingsToSave({ useGateway: false, model: typed, effort: typed }),
      { useGateway: false, model: '', effort: '' },
    );
  }
  assert.deepEqual(
    orchestratorLaunchSettingsToSave({ useGateway: true, model: 'default-x', effort: 'high' }),
    { useGateway: true, model: 'default-x', effort: 'high' },
  );
  assert.match(orchestratorModelHelper(choices(), 'Default'), /Using the default \(opus\)/);
});

// A catalog-less gateway is refused at write and at launch, so offering the
// switch would promise a save that cannot land.
test('disables the gateway switch when no catalog is configured', () => {
  const control = orchestratorGatewayControl(choices({ gatewayConfigured: false }), false);
  assert.equal(control.disabled, true);
  assert.equal(control.blocked, false);
  assert.match(control.helper, /ERun settings/);
});

// A stored "on" with no catalog cannot launch; turning it off is the remedy,
// so the switch stays usable and the state reads as a problem.
test('keeps a stored gateway-on usable when the catalog is gone', () => {
  const control = orchestratorGatewayControl(choices({ gatewayConfigured: false }), true);
  assert.equal(control.disabled, false);
  assert.equal(control.blocked, true);
  assert.match(control.helper, /cannot start/);
});

test('enables the gateway switch once a catalog is configured', () => {
  assert.equal(orchestratorGatewayControl(choices(), false).disabled, false);
  assert.equal(orchestratorGatewayControl(choices(), true).blocked, false);
});

test('holds the switch until the catalog has been read', () => {
  assert.equal(orchestratorGatewayControl(null, false).disabled, true);
  assert.equal(orchestratorGatewayControl(null, true).disabled, false);
});

test('offers the catalog models and names the catalog default', () => {
  const options = orchestratorGatewayModelOptions(choices(), '');
  assert.deepEqual(values(options), [
    ORCHESTRATOR_LAUNCH_DEFAULT,
    'deepseek/deepseek-v4.1-flash',
    'anthropic/claude-fable-5.1',
  ]);
  assert.equal(options[0]?.label, 'Default (deepseek/deepseek-v4.1-flash)');
});

// A catalog with no driveable model supplies no default, so the launch falls
// back to the orchestrator's own and the label must say so.
test('names the orchestrator default when the catalog lists none', () => {
  const options = orchestratorGatewayModelOptions(
    choices({ gatewayModels: [], gatewayDefaultModel: '' }),
    '',
  );
  assert.equal(options[0]?.label, 'Default (opus)');
});

// A model typed off the gateway, or one since removed from the catalog, must
// still render: a trigger with no matching item is blank, hiding the value the
// next save sends back.
test('keeps a stored model the catalog does not list', () => {
  const options = orchestratorGatewayModelOptions(choices(), 'opus');
  assert.equal(options[options.length - 1]?.value, 'opus');
  assert.match(options[options.length - 1]?.label ?? '', /not in the gateway catalog/);
  assert.equal(
    values(orchestratorGatewayModelOptions(choices(), 'anthropic/claude-fable-5.1')).length,
    3,
  );
});

// The catalog can name a model the gateway cannot drive. Stored, it would be
// refused at launch, so it reads as that rather than as merely unlisted.
test('flags a stored model the gateway cannot drive', () => {
  const options = orchestratorGatewayModelOptions(choices(), 'acme/reasoner');
  assert.equal(
    options[options.length - 1]?.label,
    'acme/reasoner (cannot be driven through the gateway)',
  );
  assert.equal(orchestratorGatewayModelUndriveable(choices(), 'acme/reasoner'), true);
  assert.equal(orchestratorGatewayModelUndriveable(choices(), 'opus'), false);
  assert.equal(orchestratorGatewayModelUndriveable(null, 'acme/reasoner'), false);
  assert.match(orchestratorGatewayModelHelper(choices(), 'acme/reasoner'), /cannot drive/);
  assert.match(orchestratorGatewayModelHelper(choices(), ''), /catalog's models/);
});

test('offers every effort level plus the named default', () => {
  const options = orchestratorEffortOptions(choices(), '');
  assert.deepEqual(values(options), [ORCHESTRATOR_LAUNCH_DEFAULT, ...LEVELS]);
  assert.equal(options[0]?.label, 'Default (ultracode)');
});

test('keeps a stored effort level this build does not know', () => {
  const options = orchestratorEffortOptions(choices(), 'turbo');
  assert.equal(options[options.length - 1]?.value, 'turbo');
  assert.match(options[options.length - 1]?.label ?? '', /not a known effort level/);
});

// Mirrors the Go launch guard: a value that would reach --model as a flag or
// as shell syntax is flagged where it is typed.
test('flags a model the launch guard would refuse', () => {
  assert.equal(orchestratorModelInvalid(''), false);
  assert.equal(orchestratorModelInvalid('claude-opus-4.1'), false);
  assert.equal(orchestratorModelInvalid('--dangerously-skip-permissions'), true);
  assert.equal(orchestratorModelInvalid('a b'), true);
  assert.match(orchestratorModelHelper(choices(), '-x'), /cannot start with -/);
});

test('names the default the empty model launches on', () => {
  assert.match(orchestratorModelHelper(choices(), ''), /default \(opus\)/);
  assert.match(orchestratorModelHelper(choices(), 'sonnet'), /Launches on sonnet/);
});

test('does not flag a stored value before the choices have been read', () => {
  const effort = orchestratorEffortOptions(null, 'high');
  assert.equal(effort[effort.length - 1]?.label, 'high');
  const model = orchestratorGatewayModelOptions(null, 'sonnet');
  assert.equal(model[model.length - 1]?.label, 'sonnet');
});
