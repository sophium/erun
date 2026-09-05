import {
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  Checkbox,
  FieldLabel,
  Input,
  Label,
  StatusBadge,
} from 'erun-kit';
import * as React from 'react';

import type { SmtpConfig, SmtpStatus, UpdateSmtpSettingsInput } from '../app/api/identityApi';
import type { SmtpSettingsState } from './controller';
import { useSmtpSettingsController } from './controller';

// The backend's `host` field carries "host:port" as one string (e.g.
// "smtp.example.com:587"); the form splits it into two inputs for the
// operator and rejoins them on submit.
function splitHostPort(hostPort: string): { host: string; port: string } {
  const separatorIndex = hostPort.lastIndexOf(':');
  if (separatorIndex <= 0 || separatorIndex === hostPort.length - 1) {
    return { host: hostPort, port: '' };
  }
  const port = hostPort.slice(separatorIndex + 1);
  if (!/^\d+$/.test(port)) {
    return { host: hostPort, port: '' };
  }
  return { host: hostPort.slice(0, separatorIndex), port };
}

function joinHostPort(host: string, port: string): string {
  const trimmedHost = host.trim();
  const trimmedPort = port.trim();
  return trimmedPort === '' ? trimmedHost : `${trimmedHost}:${trimmedPort}`;
}

interface SmtpFormFields {
  host: string;
  port: string;
  username: string;
  password: string;
  senderAddress: string;
  senderName: string;
  replyToAddress: string;
  tls: boolean;
  setHost: (value: string) => void;
  setPort: (value: string) => void;
  setUsername: (value: string) => void;
  setPassword: (value: string) => void;
  setSenderAddress: (value: string) => void;
  setSenderName: (value: string) => void;
  setReplyToAddress: (value: string) => void;
  setTls: (value: boolean) => void;
}

// useSmtpFormFields seeds one local field per config value the first time
// this form instance renders. Like OrgSettingsPanel's equivalent hook, it
// deliberately does not resync on every parent re-render — a failed save
// keeps the operator's own edits on screen (see SmtpSettingsState's
// 'error' case) instead of snapping back to the last-saved value.
function useSmtpFormFields(config: SmtpConfig): SmtpFormFields {
  const initial = splitHostPort(config.host);
  const [host, setHost] = React.useState(initial.host);
  const [port, setPort] = React.useState(initial.port);
  const [username, setUsername] = React.useState(config.username);
  const [password, setPassword] = React.useState('');
  const [senderAddress, setSenderAddress] = React.useState(config.senderAddress);
  const [senderName, setSenderName] = React.useState(config.senderName);
  const [replyToAddress, setReplyToAddress] = React.useState(config.replyToAddress ?? '');
  const [tls, setTls] = React.useState(config.tls);
  return {
    host,
    port,
    username,
    password,
    senderAddress,
    senderName,
    replyToAddress,
    tls,
    setHost,
    setPort,
    setUsername,
    setPassword,
    setSenderAddress,
    setSenderName,
    setReplyToAddress,
    setTls,
  };
}

function ConfiguredStatus({ configured }: { configured: boolean }): React.ReactElement {
  return configured ? (
    <StatusBadge tone="success" label="Configured" />
  ) : (
    <StatusBadge tone="muted" label="Not configured" />
  );
}

function HostFields({ fields }: { fields: SmtpFormFields }): React.ReactElement {
  return (
    <div className="grid grid-cols-[2fr_1fr] gap-2">
      <div className="grid gap-2">
        <FieldLabel htmlFor="smtp-host" required>
          Host
        </FieldLabel>
        <Input
          id="smtp-host"
          value={fields.host}
          onChange={(e) => {
            fields.setHost(e.target.value);
          }}
          required
        />
      </div>
      <div className="grid gap-2">
        <FieldLabel htmlFor="smtp-port">Port</FieldLabel>
        <Input
          id="smtp-port"
          inputMode="numeric"
          placeholder="587"
          value={fields.port}
          onChange={(e) => {
            fields.setPort(e.target.value);
          }}
        />
      </div>
    </div>
  );
}

function CredentialFields({
  fields,
  configured,
}: {
  fields: SmtpFormFields;
  configured: boolean;
}): React.ReactElement {
  return (
    <>
      <div className="grid gap-2">
        <FieldLabel htmlFor="smtp-username">Username</FieldLabel>
        <Input
          id="smtp-username"
          value={fields.username}
          onChange={(e) => {
            fields.setUsername(e.target.value);
          }}
        />
      </div>
      <div className="grid gap-2">
        <FieldLabel htmlFor="smtp-password" required={!configured}>
          Password
        </FieldLabel>
        <Input
          id="smtp-password"
          type="password"
          autoComplete="new-password"
          placeholder={configured ? 'Leave blank to keep the current password' : ''}
          value={fields.password}
          onChange={(e) => {
            fields.setPassword(e.target.value);
          }}
          required={!configured}
        />
      </div>
    </>
  );
}

function SenderFields({ fields }: { fields: SmtpFormFields }): React.ReactElement {
  return (
    <>
      <div className="grid gap-2">
        <FieldLabel htmlFor="smtp-sender-address" required>
          From address
        </FieldLabel>
        <Input
          id="smtp-sender-address"
          type="email"
          value={fields.senderAddress}
          onChange={(e) => {
            fields.setSenderAddress(e.target.value);
          }}
          required
        />
      </div>
      <div className="grid gap-2">
        <FieldLabel htmlFor="smtp-sender-name">From name</FieldLabel>
        <Input
          id="smtp-sender-name"
          value={fields.senderName}
          onChange={(e) => {
            fields.setSenderName(e.target.value);
          }}
        />
      </div>
      <div className="grid gap-2">
        <FieldLabel htmlFor="smtp-reply-to">Reply-to address</FieldLabel>
        <Input
          id="smtp-reply-to"
          type="email"
          value={fields.replyToAddress}
          onChange={(e) => {
            fields.setReplyToAddress(e.target.value);
          }}
        />
      </div>
    </>
  );
}

function SmtpForm({
  status,
  saving,
  onSave,
}: {
  status: SmtpStatus;
  saving: boolean;
  onSave: (input: UpdateSmtpSettingsInput) => void;
}): React.ReactElement {
  const fields = useSmtpFormFields(status.config);

  const submit = (event: React.SyntheticEvent): void => {
    event.preventDefault();
    onSave({
      host: joinHostPort(fields.host, fields.port),
      username: fields.username.trim() || undefined,
      password: fields.password.trim() || undefined,
      senderAddress: fields.senderAddress.trim(),
      senderName: fields.senderName.trim() || undefined,
      replyToAddress: fields.replyToAddress.trim() || undefined,
      tls: fields.tls,
    });
  };

  return (
    <form className="grid max-w-md gap-3" onSubmit={submit} aria-labelledby="smtp-form-heading">
      <div className="flex items-center justify-between">
        <h3 id="smtp-form-heading" className="text-sm font-semibold text-foreground">
          Mail server
        </h3>
        <ConfiguredStatus configured={status.configured} />
      </div>
      <HostFields fields={fields} />
      <CredentialFields fields={fields} configured={status.configured} />
      <SenderFields fields={fields} />
      <div className="flex items-center gap-2">
        <Checkbox
          id="smtp-tls"
          checked={fields.tls}
          onCheckedChange={(value) => {
            fields.setTls(value === true);
          }}
        />
        <Label htmlFor="smtp-tls">Use TLS</Label>
      </div>
      <Button type="submit" disabled={saving} className="justify-self-start">
        {saving ? 'Saving…' : 'Save settings'}
      </Button>
    </form>
  );
}

function SmtpSettingsBody({
  state,
  onSave,
}: {
  state: SmtpSettingsState;
  onSave: (input: UpdateSmtpSettingsInput) => void;
}): React.ReactElement {
  if (state.status === 'loading') {
    return (
      <p className="text-sm text-muted-foreground" role="status">
        Loading mail settings…
      </p>
    );
  }
  if (state.status === 'load-error') {
    return (
      <p className="text-sm text-destructive" role="alert">
        Could not load mail settings: {state.message}
      </p>
    );
  }
  return (
    <div className="grid gap-3">
      {state.status === 'error' && (
        <p className="text-sm text-destructive" role="alert">
          Could not save mail settings: {state.message}
        </p>
      )}
      <SmtpForm status={state.settings} saving={state.status === 'saving'} onSave={onSave} />
    </div>
  );
}

// SmtpSettingsPanel is the console's view of the platform's outbound-mail
// configuration (issue #1168): every flow that reaches a person out of band
// (signup verification, password reset, the enrollment invite in
// UsersPanel) depends on it, and a freshly deployed platform starts with
// none configured. Only rendered for an OPERATIONS tenant — see App.tsx.
export function SmtpSettingsPanel({ token }: { token: string }): React.ReactElement {
  const { state, save } = useSmtpSettingsController(token);
  return (
    <Card aria-labelledby="smtp-settings-heading">
      <CardHeader>
        <CardTitle id="smtp-settings-heading">Outbound mail (SMTP)</CardTitle>
      </CardHeader>
      <CardContent>
        <p className="mb-4 text-sm text-muted-foreground">
          Bring your own provider&apos;s credentials — ERun does not supply a mail provider on your
          behalf. Until this is configured, enrolling a user falls back to a temporary password
          instead of an invite email.
        </p>
        <SmtpSettingsBody state={state} onSave={save} />
      </CardContent>
    </Card>
  );
}
