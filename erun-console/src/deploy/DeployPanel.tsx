import * as React from 'react';

import type { Environment } from '../config/types';
import type { EnvDeployState } from './controller';
import { useDeployController } from './controller';

// "Deploy a runtime environment" panel: a thin render layer over the deploy
// controller. For each runtime environment it offers a Deploy action (with an
// optional version override, defaulting to the env's persisted runtimeVersion)
// and surfaces the live deploy status (the controller polls
// GET /v1/environments/{id} until deployed/failed). No business logic lives
// here — every request goes through the controller, which calls the typed
// client. Only `runtime` envs are shown: deploy installs the runtime chart.

// persistedStatusLine renders the env's persisted deploy lifecycle (from the
// read model) when there is no in-session deploy yet — so opening the console
// after a prior deploy shows its outcome (especially a failure) on first paint,
// not a bare Deploy button.
function persistedStatusLine(environment: Environment): string {
  switch (environment.deployStatus) {
    case 'deployed':
      return `${environment.name} is deployed${versionSuffix(environment)}.`;
    case 'failed':
      return `${environment.name} failed to deploy.`;
    case 'deploying':
      return `Deploying ${environment.name}…`;
    default:
      return '';
  }
}

function statusLine(state: EnvDeployState | undefined, environment: Environment): string {
  if (state === undefined) {
    return persistedStatusLine(environment);
  }
  switch (state.status) {
    case 'starting':
      return 'Starting deploy…';
    case 'deploying':
      return `Deploying ${environment.name}…`;
    case 'deployed':
      return `${environment.name} is deployed${versionSuffix(state.environment)}.`;
    case 'failed':
      return `${environment.name} failed to deploy.`;
    case 'error':
      return `Request failed: ${state.message}`;
  }
}

function versionSuffix(environment: Environment): string {
  return environment.deployedVersion !== undefined ? ` (${environment.deployedVersion})` : '';
}

// failureReason returns the deploy error to show alongside a failed line, from
// the in-session state when present, else the env's persisted deployError so a
// prior failure's reason survives a page reload.
function failureReason(state: EnvDeployState | undefined, environment: Environment): string | undefined {
  if (state?.status === 'failed') {
    return state.environment.deployError;
  }
  if (state === undefined && environment.deployStatus === 'failed') {
    return environment.deployError;
  }
  return undefined;
}

function feedbackRole(state: EnvDeployState | undefined, environment: Environment): 'status' | 'alert' {
  if (state === undefined) {
    return environment.deployStatus === 'failed' ? 'alert' : 'status';
  }
  return state.status === 'failed' || state.status === 'error' ? 'alert' : 'status';
}

function DeployStatus({
  state,
  environment,
}: {
  state: EnvDeployState | undefined;
  environment: Environment;
}): React.ReactElement | null {
  const line = statusLine(state, environment);
  if (line === '') {
    return null;
  }
  const reason = failureReason(state, environment);
  return (
    <div className="deploy-status" role={feedbackRole(state, environment)} aria-live="polite">
      <p>{line}</p>
      {reason !== undefined && <p className="context-error">{reason}</p>}
    </div>
  );
}

function EnvironmentDeployRow({
  environment,
  state,
  onDeploy,
}: {
  environment: Environment;
  state: EnvDeployState | undefined;
  onDeploy: (environmentId: string, version: string) => void;
}): React.ReactElement {
  const [version, setVersion] = React.useState(environment.runtimeVersion ?? '');
  const busy = state?.status === 'starting' || state?.status === 'deploying';
  const versionInputId = `deploy-version-${environment.environmentId}`;
  return (
    <li className="deploy-row">
      <span className="deploy-env-name">{environment.name}</span>
      <label htmlFor={versionInputId}>Version</label>
      <input
        id={versionInputId}
        value={version}
        onChange={(e) => {
          setVersion(e.target.value);
        }}
        placeholder="runtime version"
      />
      <button
        type="button"
        disabled={busy}
        onClick={() => {
          onDeploy(environment.environmentId, version.trim());
        }}
      >
        {busy ? 'Deploying…' : 'Deploy'}
      </button>
      <DeployStatus state={state} environment={environment} />
    </li>
  );
}

export function DeployPanel({
  token,
  environments,
}: {
  token: string;
  environments: Environment[];
}): React.ReactElement {
  const { states, deploy } = useDeployController(token);
  const runtimeEnvironments = environments.filter((environment) => environment.type === 'runtime');
  return (
    <section className="deploy-panel" aria-labelledby="deploy-heading">
      <h2 id="deploy-heading">Deploy a runtime environment</h2>
      {runtimeEnvironments.length === 0 ? (
        <p className="empty-state">No runtime environments to deploy.</p>
      ) : (
        <ul className="deploy-list">
          {runtimeEnvironments.map((environment) => (
            <EnvironmentDeployRow
              key={environment.environmentId}
              environment={environment}
              state={states[environment.environmentId]}
              onDeploy={deploy}
            />
          ))}
        </ul>
      )}
    </section>
  );
}
