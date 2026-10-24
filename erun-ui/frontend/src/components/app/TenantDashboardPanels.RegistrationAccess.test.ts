import assert from 'node:assert/strict';

import { Tabs } from 'erun-kit';
import * as React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { Provider } from 'react-redux';
import { test } from 'vitest';

import { store } from '@/app/store';
import type { UITenantDashboard } from '@/types';

import { RegistrationPanel } from './TenantDashboardPanels.Registration';
import { RegistrationEnvironmentsSection } from './TenantDashboardPanels.RegistrationEnvironments';

// Capable-caller cases are the positive control for the absence assertions.

function dashboard(overrides: Partial<UITenantDashboard> = {}): UITenantDashboard {
  return {
    tenant: 'frs',
    contexts: [],
    environments: [],
    canCreateReview: false,
    canAdvanceMergeQueue: false,
    canOverrideMergeQueue: false,
    canCreateContext: false,
    canRegisterEnvironment: false,
    canDeployEnvironment: false,
    canStopEnvironment: false,
    canDeleteEnvironment: false,
    canApproveInviteRequests: false,
    canDeclineInviteRequests: false,
    ...overrides,
  };
}

function render(element: React.ReactElement): string {
  return renderToStaticMarkup(React.createElement(Provider, { store, children: element }));
}

function renderEnvironments(data: UITenantDashboard): string {
  return render(React.createElement(RegistrationEnvironmentsSection, { data }));
}

function renderRegistrationTab(data: UITenantDashboard): string {
  return render(
    React.createElement(
      Tabs,
      { defaultValue: 'registration' },
      React.createElement(RegistrationPanel, { data }),
    ),
  );
}

test('a caller who may not register is told which access the hosted-environments form needs', () => {
  const markup = renderEnvironments(dashboard({ canRegisterEnvironment: false }));
  assert.ok(
    markup.includes('registering a hosted environment needs additional access'),
    'the missing register access is named on the tab',
  );
});

test('an empty hosted-environments list does not name a form the caller cannot see', () => {
  const markup = renderEnvironments(dashboard({ canRegisterEnvironment: false }));
  assert.ok(
    !markup.includes('Preview a plan above'),
    'the empty state must not promise the preview form that is not rendered',
  );
  assert.ok(
    !markup.includes('Preview plan'),
    'the preview control itself is absent, so the copy cannot name it',
  );
});

test('an empty hosted-environments list keeps its instruction while the form is on screen', () => {
  const markup = renderEnvironments(dashboard({ canRegisterEnvironment: true }));
  assert.ok(
    markup.includes('Preview a plan above'),
    'a caller who may register is still pointed at the form above',
  );
  assert.ok(markup.includes('Preview plan'), 'the form above is the control the copy names');
});

test('a caller who may not register is told which access the cloud-context form needs', () => {
  const markup = renderRegistrationTab(dashboard({ canCreateContext: false }));
  assert.ok(
    markup.includes('registering a cloud context needs additional access'),
    'the missing context-create access is named on the tab',
  );
});

test('an empty cloud-context list does not point below at a form the caller cannot see', () => {
  const markup = renderRegistrationTab(dashboard({ canCreateContext: false }));
  assert.ok(
    !markup.includes('Register one below'),
    'the empty state must not point at the absent context form',
  );
  assert.ok(
    !markup.includes('Register a cloud context'),
    'the context form heading is absent, so the copy cannot name it',
  );
});

test('an empty cloud-context list keeps its instruction while the form is on screen', () => {
  const markup = renderRegistrationTab(dashboard({ canCreateContext: true }));
  assert.ok(
    markup.includes('Register one below'),
    'a caller who may create a context is still pointed at the form below',
  );
  assert.ok(
    markup.includes('Register a cloud context'),
    'the form below is the control the copy names',
  );
});
