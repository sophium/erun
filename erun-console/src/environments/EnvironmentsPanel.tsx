import {
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  FieldLabel,
  Input,
  SelectField,
} from 'erun-kit';
import * as React from 'react';

import { type CloudContext, type Environment, isTearingDown } from '../config/types';
import type { DeployState, RegisterState } from './controller';
import { useDeployController, useRegisterEnvironmentController } from './controller';

// "Register and deploy environments" panel: register a new environment, and deploy an
// already-registered runtime one at a chosen or default version. A thin render
// layer over the two controllers — no business logic lives here.

const ENV_TYPES = ['runtime', 'remote-agent', 'local-agent'];
// Radix Select items cannot carry an empty-string value, so "no cloud context"
// needs a sentinel distinct from every real contextId.
const NO_CONTEXT = '__none__';

function RegisterFeedback({ state }: { state: RegisterState }): React.ReactElement | null {
  if (state.status === 'created') {
    return (
      <p className="text-sm text-muted-foreground" role="status">
        Environment {state.environment.name} registered.
      </p>
    );
  }
  if (state.status === 'error') {
    return (
      <p className="text-sm text-destructive" role="alert">
        Could not register environment: {state.message}
      </p>
    );
  }
  return null;
}

interface RegisterFormValues {
  name: string;
  type: string;
  contextId: string;
  kubernetesContext: string;
  runtimeVersion: string;
}

function contextOptions(contexts: CloudContext[]): { value: string; label: string }[] {
  return [
    { value: NO_CONTEXT, label: '— none —' },
    ...contexts.map((context) => ({
      value: context.contextId,
      label: context.status !== undefined ? `${context.name} (${context.status})` : context.name,
    })),
  ];
}

function RegisterForm({
  contexts,
  state,
  onRegister,
}: {
  contexts: CloudContext[];
  state: RegisterState;
  onRegister: (values: RegisterFormValues) => void;
}): React.ReactElement {
  const [name, setName] = React.useState('');
  const [type, setType] = React.useState('runtime');
  const [contextId, setContextId] = React.useState(NO_CONTEXT);
  const [kubernetesContext, setKubernetesContext] = React.useState('');
  const [runtimeVersion, setRuntimeVersion] = React.useState('');
  const busy = state.status === 'creating';

  const submit = (event: React.SyntheticEvent): void => {
    event.preventDefault();
    onRegister({
      name,
      type,
      contextId: contextId === NO_CONTEXT ? '' : contextId,
      kubernetesContext,
      runtimeVersion,
    });
  };

  return (
    <form className="grid max-w-md gap-3" onSubmit={submit} aria-labelledby="register-env-heading">
      <h3 id="register-env-heading" className="text-sm font-semibold text-foreground">
        Register an environment
      </h3>
      <div className="grid gap-2">
        <FieldLabel htmlFor="env-name" required>
          Name
        </FieldLabel>
        <Input
          id="env-name"
          value={name}
          onChange={(e) => {
            setName(e.target.value);
          }}
          required
        />
      </div>
      <SelectField
        id="env-type"
        label="Type"
        value={type}
        options={ENV_TYPES.map((option) => ({ value: option, label: option }))}
        onChange={setType}
      />
      <SelectField
        id="env-context"
        label="Cloud context"
        value={contextId}
        options={contextOptions(contexts)}
        onChange={setContextId}
      />
      <div className="grid gap-2">
        <FieldLabel htmlFor="env-kubernetes-context">Kubernetes context (optional)</FieldLabel>
        <Input
          id="env-kubernetes-context"
          value={kubernetesContext}
          onChange={(e) => {
            setKubernetesContext(e.target.value);
          }}
        />
      </div>
      <div className="grid gap-2">
        <FieldLabel htmlFor="env-runtime-version">Runtime version (optional)</FieldLabel>
        <Input
          id="env-runtime-version"
          value={runtimeVersion}
          onChange={(e) => {
            setRuntimeVersion(e.target.value);
          }}
          placeholder="1.2.3"
        />
      </div>
      <Button type="submit" disabled={busy} className="justify-self-start">
        {busy ? 'Registering…' : 'Register environment'}
      </Button>
      <RegisterFeedback state={state} />
    </form>
  );
}

// versionSummary renders the deployed version alongside the declared pin when
// they differ, so a stale deploy is visible rather than looking up to date.
function versionSummary(environment: Environment): string {
  const deployed = environment.deployedVersion;
  if (deployed === undefined) {
    return '';
  }
  const pinned = environment.runtimeVersion;
  return pinned !== undefined && pinned !== deployed
    ? ` — deployed ${deployed} (pinned ${pinned})`
    : ` (${deployed})`;
}

function deployStatusLine(state: DeployState | undefined, environment: Environment): string {
  if (state === undefined) {
    return '';
  }
  switch (state.status) {
    case 'starting':
      return 'Starting deploy…';
    case 'deploying':
      return `Deploying ${environment.name}…`;
    case 'running':
      return `${environment.name} is running${versionSummary(state.environment)}.`;
    case 'failed':
      return `${environment.name} failed to deploy.`;
    case 'conflict':
      return `A deploy is already in progress for ${environment.name}.`;
    case 'unavailable':
      return 'The deploy executor is not configured on this control plane.';
    case 'error':
      return `Request failed: ${state.message}`;
  }
}

// deployFeedbackRole treats the conflict/unavailable states as their own kind
// of expected-but-blocked state (status), distinct from a genuine failure
// (alert) — a 409 is not an error the operator caused, unlike a 501 misconfig.
function deployFeedbackRole(state: DeployState): 'status' | 'alert' {
  return state.status === 'failed' || state.status === 'error' || state.status === 'unavailable'
    ? 'alert'
    : 'status';
}

function DeployStatus({
  state,
  environment,
}: {
  state: DeployState | undefined;
  environment: Environment;
}): React.ReactElement | null {
  const line = deployStatusLine(state, environment);
  if (line === '' || state === undefined) {
    return null;
  }
  // The full provisionError string is shown, not truncated — it names the
  // version, the registries probed, and the ways out.
  const reason = state.status === 'failed' ? state.environment.provisionError : undefined;
  return (
    <div
      className="text-sm text-muted-foreground"
      role={deployFeedbackRole(state)}
      aria-live="polite"
    >
      <p>{line}</p>
      {reason !== undefined && <p className="text-xs text-destructive">{reason}</p>}
    </div>
  );
}

// TeardownRow replaces the whole deploy control for an environment whose delete
// is outstanding. The API refuses a deploy on these with 409, so offering the
// button meant an operator clicked it and got a raw Kubernetes admission error
// back (#1170) — and before the API guard existed, the deploy was accepted and
// overwrote the teardown state. Showing the recorded blocker instead is the
// thing an operator can actually act on: it names the finalizer holding the
// namespace.
function TeardownRow({ environment }: { environment: Environment }): React.ReactElement {
  const deleting = environment.status === 'deleting';
  return (
    <li className="grid gap-1 border-b border-border py-3 last:border-b-0">
      <span className="font-medium text-foreground">{environment.name}</span>
      <div className="text-sm text-muted-foreground" role="status" aria-live="polite">
        <p>
          {deleting
            ? `${environment.name} is being deleted, so it cannot be deployed.`
            : `${environment.name}'s delete is blocked and still outstanding, so it cannot be deployed. Resolve the teardown first.`}
        </p>
        {environment.deleteError !== undefined && (
          <p className="text-xs text-destructive">{environment.deleteError}</p>
        )}
      </div>
    </li>
  );
}

function EnvironmentDeployRow({
  environment,
  state,
  onDeploy,
}: {
  environment: Environment;
  state: DeployState | undefined;
  onDeploy: (environmentId: string, version: string) => void;
}): React.ReactElement {
  const [version, setVersion] = React.useState('');
  const busy = state?.status === 'starting' || state?.status === 'deploying';
  const versionInputId = `deploy-version-${environment.environmentId}`;
  if (isTearingDown(environment)) {
    return <TeardownRow environment={environment} />;
  }
  return (
    <li className="grid gap-2 border-b border-border py-3 last:border-b-0">
      <span className="font-medium text-foreground">{environment.name}</span>
      <div className="flex items-end gap-2">
        <div className="grid gap-2">
          <FieldLabel htmlFor={versionInputId}>Version</FieldLabel>
          <Input
            id={versionInputId}
            value={version}
            onChange={(e) => {
              setVersion(e.target.value);
            }}
            placeholder={environment.runtimeVersion ?? 'pinned version'}
          />
        </div>
        <Button
          type="button"
          disabled={busy}
          onClick={() => {
            onDeploy(environment.environmentId, version.trim());
          }}
        >
          {busy ? 'Deploying…' : 'Deploy'}
        </Button>
      </div>
      <DeployStatus state={state} environment={environment} />
    </li>
  );
}

function DeployList({
  environments,
  states,
  onDeploy,
}: {
  environments: Environment[];
  states: Record<string, DeployState>;
  onDeploy: (environmentId: string, version: string) => void;
}): React.ReactElement {
  const runtimeEnvironments = environments.filter((environment) => environment.type === 'runtime');
  if (runtimeEnvironments.length === 0) {
    return <p className="text-sm text-muted-foreground">No runtime environments to deploy.</p>;
  }
  return (
    <ul>
      {runtimeEnvironments.map((environment) => (
        <EnvironmentDeployRow
          key={environment.environmentId}
          environment={environment}
          state={states[environment.environmentId]}
          onDeploy={onDeploy}
        />
      ))}
    </ul>
  );
}

export function EnvironmentsPanel({
  token,
  contexts,
  environments,
  onChanged,
}: {
  token: string;
  contexts: CloudContext[];
  environments: Environment[];
  onChanged: () => void;
}): React.ReactElement {
  const { state, register } = useRegisterEnvironmentController(token, onChanged);
  const { states, deploy } = useDeployController(token, onChanged);

  return (
    <Card aria-labelledby="environments-panel-heading">
      <CardHeader>
        <CardTitle id="environments-panel-heading">Register and deploy environments</CardTitle>
      </CardHeader>
      <CardContent className="grid gap-6">
        <RegisterForm
          contexts={contexts}
          state={state}
          onRegister={(values) => {
            register({
              name: values.name.trim(),
              type: values.type,
              contextId: values.contextId.trim() === '' ? undefined : values.contextId.trim(),
              kubernetesContext:
                values.kubernetesContext.trim() === ''
                  ? undefined
                  : values.kubernetesContext.trim(),
              runtimeVersion:
                values.runtimeVersion.trim() === '' ? undefined : values.runtimeVersion.trim(),
            });
          }}
        />
        <div>
          <h3 className="mb-2 text-sm font-semibold text-foreground">
            Deploy a runtime environment
          </h3>
          <DeployList environments={environments} states={states} onDeploy={deploy} />
        </div>
      </CardContent>
    </Card>
  );
}
