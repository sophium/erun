import {
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  FieldLabel,
  Input,
  Textarea,
} from 'erun-kit';
import * as React from 'react';

import type { AliasState, ProvisionState } from './controller';
import { useProvisionController } from './controller';

const DEFAULT_PROVIDER = 'aws';

function AliasFeedback({ alias }: { alias: AliasState }): React.ReactElement | null {
  if (alias.status === 'saved') {
    return (
      <p className="text-sm text-muted-foreground" role="status">
        Credentials saved (encrypted server-side).
      </p>
    );
  }
  if (alias.status === 'error') {
    return (
      <p className="text-sm text-destructive" role="alert">
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
    <form className="grid max-w-md gap-3" onSubmit={submit} aria-labelledby="alias-form-heading">
      <h3 id="alias-form-heading" className="text-sm font-semibold text-foreground">
        Register cloud credentials
      </h3>
      <div className="grid gap-2">
        <FieldLabel htmlFor="alias-name" required>
          Alias name
        </FieldLabel>
        <Input
          id="alias-name"
          value={name}
          onChange={(e) => {
            setName(e.target.value);
          }}
          required
        />
      </div>
      <div className="grid gap-2">
        <FieldLabel htmlFor="alias-provider">Provider</FieldLabel>
        <Input
          id="alias-provider"
          value={provider}
          onChange={(e) => {
            setProvider(e.target.value);
          }}
        />
      </div>
      <div className="grid gap-2">
        <FieldLabel htmlFor="alias-credentials" required>
          BYO-cloud credentials JSON (stored encrypted server-side)
        </FieldLabel>
        <Textarea
          id="alias-credentials"
          value={credentials}
          onChange={(e) => {
            setCredentials(e.target.value);
          }}
          rows={4}
          placeholder={'{"accessKeyId":"…","secretAccessKey":"…"}'}
          required
        />
      </div>
      <Button type="submit" disabled={saving} className="justify-self-start">
        {saving ? 'Saving…' : 'Save credentials'}
      </Button>
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
    <div
      className="text-sm text-muted-foreground"
      role={feedbackRole(provision)}
      aria-live="polite"
    >
      <p>{statusLine(provision)}</p>
      {failureReason !== undefined && <p className="text-xs text-destructive">{failureReason}</p>}
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
    <form className="grid max-w-md gap-3" onSubmit={submit} aria-labelledby="context-form-heading">
      <h3 id="context-form-heading" className="text-sm font-semibold text-foreground">
        Provision a cloud context
      </h3>
      <div className="grid gap-2">
        <FieldLabel htmlFor="context-name" required>
          Context name
        </FieldLabel>
        <Input
          id="context-name"
          value={name}
          onChange={(e) => {
            setName(e.target.value);
          }}
          required
        />
      </div>
      <div className="grid gap-2">
        <FieldLabel htmlFor="context-alias" required>
          Cloud provider alias
        </FieldLabel>
        <Input
          id="context-alias"
          value={cloudProviderAlias}
          onChange={(e) => {
            setAlias(e.target.value);
          }}
          required
        />
      </div>
      <div className="grid gap-2">
        <FieldLabel htmlFor="context-region" required>
          Region
        </FieldLabel>
        <Input
          id="context-region"
          value={region}
          onChange={(e) => {
            setRegion(e.target.value);
          }}
          required
        />
      </div>
      <Button type="submit" disabled={busy} className="justify-self-start">
        {busy ? 'Provisioning…' : 'Provision context'}
      </Button>
      <ProvisionStatus provision={provision} />
    </form>
  );
}

export function ProvisionPanel({ token }: { token: string }): React.ReactElement {
  const { alias, provision, saveAlias, provisionContext } = useProvisionController(token);
  return (
    <Card aria-labelledby="provision-heading">
      <CardHeader>
        <CardTitle id="provision-heading">Provision a cloud context</CardTitle>
      </CardHeader>
      <CardContent className="grid gap-6">
        <AliasForm alias={alias} onSave={saveAlias} />
        <CreateContextForm provision={provision} onProvision={provisionContext} />
      </CardContent>
    </Card>
  );
}
