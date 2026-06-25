import * as React from 'react';

import type { CloudContext } from '../config/types';
import type { RegisterEnvState } from './controller';
import { useRegisterEnvController } from './controller';

// "Register an environment" panel: a thin render layer over the env-registration
// controller. It collects the env name, type, the context it runs in (picked
// from the tenant's cloud contexts), and an optional runtime version, then posts
// it. On success the parent refreshes the read model so the new env appears in
// the config view + the deploy panel — closing the alias → context → register
// env → deploy loop inside the console. No business logic lives here.

const ENV_TYPES = ['runtime', 'remote-agent', 'local-agent'];

function RegisterEnvFeedback({ state }: { state: RegisterEnvState }): React.ReactElement | null {
  if (state.status === 'created') {
    return (
      <p className="provision-feedback provision-feedback--ok" role="status">
        Environment {state.environment.name} registered.
      </p>
    );
  }
  if (state.status === 'error') {
    return (
      <p className="provision-feedback provision-feedback--error" role="alert">
        Could not register environment: {state.message}
      </p>
    );
  }
  return null;
}

function ContextOptions({ contexts }: { contexts: CloudContext[] }): React.ReactElement {
  return (
    <>
      {contexts.map((context) => (
        <option key={context.contextId} value={context.contextId}>
          {context.name}
          {context.status !== undefined ? ` (${context.status})` : ''}
        </option>
      ))}
    </>
  );
}

export function RegisterEnvPanel({
  token,
  contexts,
  onRegistered,
}: {
  token: string;
  contexts: CloudContext[];
  onRegistered: () => void;
}): React.ReactElement {
  const { state, register } = useRegisterEnvController(token, onRegistered);
  const [name, setName] = React.useState('');
  const [type, setType] = React.useState('runtime');
  const [contextId, setContextId] = React.useState('');
  const [runtimeVersion, setRuntimeVersion] = React.useState('');
  const busy = state.status === 'creating';

  const submit = (event: React.SyntheticEvent): void => {
    event.preventDefault();
    register({
      name: name.trim(),
      type,
      contextId: contextId.trim() === '' ? undefined : contextId.trim(),
      runtimeVersion: runtimeVersion.trim() === '' ? undefined : runtimeVersion.trim(),
    });
  };

  return (
    <section className="provision-panel" aria-labelledby="register-env-heading">
      <h2 id="register-env-heading">Register an environment</h2>
      <form className="provision-form" onSubmit={submit} aria-labelledby="register-env-heading">
        <label htmlFor="env-name">Name</label>
        <input
          id="env-name"
          value={name}
          onChange={(e) => {
            setName(e.target.value);
          }}
          required
        />
        <label htmlFor="env-type">Type</label>
        <select
          id="env-type"
          value={type}
          onChange={(e) => {
            setType(e.target.value);
          }}
        >
          {ENV_TYPES.map((option) => (
            <option key={option} value={option}>
              {option}
            </option>
          ))}
        </select>
        <label htmlFor="env-context">Cloud context</label>
        <select
          id="env-context"
          value={contextId}
          onChange={(e) => {
            setContextId(e.target.value);
          }}
        >
          <option value="">— none —</option>
          <ContextOptions contexts={contexts} />
        </select>
        <label htmlFor="env-runtime-version">Runtime version (optional)</label>
        <input
          id="env-runtime-version"
          value={runtimeVersion}
          onChange={(e) => {
            setRuntimeVersion(e.target.value);
          }}
          placeholder="1.2.3"
        />
        <button type="submit" disabled={busy}>
          {busy ? 'Registering…' : 'Register environment'}
        </button>
        <RegisterEnvFeedback state={state} />
      </form>
    </section>
  );
}
