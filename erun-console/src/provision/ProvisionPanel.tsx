import * as React from 'react';

import type { AliasState, ProvisionState } from './controller';
import { useProvisionController } from './controller';

const DEFAULT_PROVIDER = 'aws';

function AliasFeedback({ alias }: { alias: AliasState }): React.ReactElement | null {
  if (alias.status === 'saved') {
    return (
      <p className="provision-feedback provision-feedback--ok" role="status">
        Credentials saved (encrypted server-side).
      </p>
    );
  }
  if (alias.status === 'error') {
    return (
      <p className="provision-feedback provision-feedback--error" role="alert">
        Could not save credentials: {alias.message}
      </p>
    );
  }
  return null;
}

function AliasForm({
  alias,
  onSave,
}: {
  alias: AliasState;
  onSave: (alias: string, provider: string, credentials: string) => void;
}): React.ReactElement {
  const [name, setName] = React.useState('');
  const [provider, setProvider] = React.useState(DEFAULT_PROVIDER);
  const [credentials, setCredentials] = React.useState('');
  const saving = alias.status === 'saving';

  const submit = (event: React.SyntheticEvent): void => {
    event.preventDefault();
    onSave(name.trim(), provider.trim(), credentials);
  };

  return (
    <form className="provision-form" onSubmit={submit} aria-labelledby="alias-form-heading">
      <h3 id="alias-form-heading">Register cloud credentials</h3>
      <label htmlFor="alias-name">Alias name</label>
      <input
        id="alias-name"
        value={name}
        onChange={(e) => {
          setName(e.target.value);
        }}
        required
      />
      <label htmlFor="alias-provider">Provider</label>
      <input
        id="alias-provider"
        value={provider}
        onChange={(e) => {
          setProvider(e.target.value);
        }}
      />
      <label htmlFor="alias-credentials">
        BYO-cloud credentials JSON (stored encrypted server-side)
      </label>
      <textarea
        id="alias-credentials"
        value={credentials}
        onChange={(e) => {
          setCredentials(e.target.value);
        }}
        rows={4}
        placeholder={'{"accessKeyId":"…","secretAccessKey":"…"}'}
        required
      />
      <button type="submit" disabled={saving}>
        {saving ? 'Saving…' : 'Save credentials'}
      </button>
      <AliasFeedback alias={alias} />
    </form>
  );
}

function statusLine(provision: ProvisionState): string {
  switch (provision.status) {
    case 'creating':
      return 'Registering context…';
    case 'polling':
      return `Provisioning ${provision.context.name}…`;
    case 'running':
      return `${provision.context.name} is running.`;
    case 'failed':
      return `${provision.context.name} failed to provision.`;
    case 'error':
      return `Request failed: ${provision.message}`;
    case 'idle':
      return '';
  }
}

function feedbackRole(provision: ProvisionState): 'status' | 'alert' {
  return provision.status === 'failed' || provision.status === 'error' ? 'alert' : 'status';
}

function ProvisionStatus({ provision }: { provision: ProvisionState }): React.ReactElement | null {
  if (provision.status === 'idle') {
    return null;
  }
  const failureReason =
    provision.status === 'failed' ? provision.context.provisionError : undefined;
  return (
    <div className="provision-status" role={feedbackRole(provision)} aria-live="polite">
      <p>{statusLine(provision)}</p>
      {failureReason !== undefined && <p className="context-error">{failureReason}</p>}
    </div>
  );
}

function CreateContextForm({
  provision,
  onProvision,
}: {
  provision: ProvisionState;
  onProvision: (input: { name: string; cloudProviderAlias: string; region: string }) => void;
}): React.ReactElement {
  const [name, setName] = React.useState('');
  const [cloudProviderAlias, setAlias] = React.useState('');
  const [region, setRegion] = React.useState('');
  const busy = provision.status === 'creating' || provision.status === 'polling';

  const submit = (event: React.SyntheticEvent): void => {
    event.preventDefault();
    onProvision({
      name: name.trim(),
      cloudProviderAlias: cloudProviderAlias.trim(),
      region: region.trim(),
    });
  };

  return (
    <form className="provision-form" onSubmit={submit} aria-labelledby="context-form-heading">
      <h3 id="context-form-heading">Provision a cloud context</h3>
      <label htmlFor="context-name">Context name</label>
      <input
        id="context-name"
        value={name}
        onChange={(e) => {
          setName(e.target.value);
        }}
        required
      />
      <label htmlFor="context-alias">Cloud provider alias</label>
      <input
        id="context-alias"
        value={cloudProviderAlias}
        onChange={(e) => {
          setAlias(e.target.value);
        }}
        required
      />
      <label htmlFor="context-region">Region</label>
      <input
        id="context-region"
        value={region}
        onChange={(e) => {
          setRegion(e.target.value);
        }}
        required
      />
      <button type="submit" disabled={busy}>
        {busy ? 'Provisioning…' : 'Provision context'}
      </button>
      <ProvisionStatus provision={provision} />
    </form>
  );
}

export function ProvisionPanel({ token }: { token: string }): React.ReactElement {
  const { alias, provision, saveAlias, provisionContext } = useProvisionController(token);
  return (
    <section className="provision-panel" aria-labelledby="provision-heading">
      <h2 id="provision-heading">Provision a cloud context</h2>
      <AliasForm alias={alias} onSave={saveAlias} />
      <CreateContextForm provision={provision} onProvision={provisionContext} />
    </section>
  );
}
