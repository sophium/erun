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
} from 'erun-kit';
import * as React from 'react';

import type { OrgSettings, UpdateOrgSettingsInput } from '../app/api/identityApi';
import type { OrgSettingsState } from './controller';
import { useOrgSettingsController } from './controller';

function DomainsList({ domains }: { domains: string[] }): React.ReactElement {
  if (domains.length === 0) {
    return <p className="text-sm text-muted-foreground">No verified domains.</p>;
  }
  return (
    <ul className="list-disc pl-5 text-sm text-muted-foreground">
      {domains.map((domain) => (
        <li key={domain}>{domain}</li>
      ))}
    </ul>
  );
}

function PolicyCheckbox({
  id,
  label,
  checked,
  onChange,
}: {
  id: string;
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
}): React.ReactElement {
  return (
    <div className="flex items-center gap-2">
      <Checkbox
        id={id}
        checked={checked}
        onCheckedChange={(value) => {
          onChange(value === true);
        }}
      />
      <Label htmlFor={id}>{label}</Label>
    </div>
  );
}

// usePasswordComplexityFields holds the four boolean password-complexity
// toggles as one unit, so SettingsForm only has one hook call (and one
// destructure) to account for instead of four repeated triples.
function usePasswordComplexityFields(settings: OrgSettings): {
  uppercase: boolean;
  lowercase: boolean;
  number: boolean;
  symbol: boolean;
  setUppercase: (value: boolean) => void;
  setLowercase: (value: boolean) => void;
  setNumber: (value: boolean) => void;
  setSymbol: (value: boolean) => void;
} {
  const [uppercase, setUppercase] = React.useState(settings.passwordRequiresUppercase);
  const [lowercase, setLowercase] = React.useState(settings.passwordRequiresLowercase);
  const [number, setNumber] = React.useState(settings.passwordRequiresNumber);
  const [symbol, setSymbol] = React.useState(settings.passwordRequiresSymbol);
  return { uppercase, lowercase, number, symbol, setUppercase, setLowercase, setNumber, setSymbol };
}

function PasswordComplexityFields({
  fields,
}: {
  fields: ReturnType<typeof usePasswordComplexityFields>;
}): React.ReactElement {
  return (
    <>
      <PolicyCheckbox
        id="org-requires-uppercase"
        label="Require an uppercase letter"
        checked={fields.uppercase}
        onChange={fields.setUppercase}
      />
      <PolicyCheckbox
        id="org-requires-lowercase"
        label="Require a lowercase letter"
        checked={fields.lowercase}
        onChange={fields.setLowercase}
      />
      <PolicyCheckbox
        id="org-requires-number"
        label="Require a number"
        checked={fields.number}
        onChange={fields.setNumber}
      />
      <PolicyCheckbox
        id="org-requires-symbol"
        label="Require a symbol"
        checked={fields.symbol}
        onChange={fields.setSymbol}
      />
    </>
  );
}

function SettingsForm({
  settings,
  saving,
  onSave,
}: {
  settings: OrgSettings;
  saving: boolean;
  onSave: (input: UpdateOrgSettingsInput) => void;
}): React.ReactElement {
  const [forceMfa, setForceMfa] = React.useState(settings.forceMfa);
  const [allowRegister, setAllowRegister] = React.useState(settings.allowRegister);
  const [minLength, setMinLength] = React.useState(settings.minPasswordLength);
  const complexity = usePasswordComplexityFields(settings);

  const submit = (event: React.SyntheticEvent): void => {
    event.preventDefault();
    onSave({
      forceMfa,
      allowRegister,
      minPasswordLength: minLength,
      passwordRequiresUppercase: complexity.uppercase,
      passwordRequiresLowercase: complexity.lowercase,
      passwordRequiresNumber: complexity.number,
      passwordRequiresSymbol: complexity.symbol,
    });
  };

  return (
    <form
      className="grid max-w-md gap-3"
      onSubmit={submit}
      aria-labelledby="org-settings-form-heading"
    >
      <h3 id="org-settings-form-heading" className="text-sm font-semibold text-foreground">
        Login and password policy
      </h3>
      <PolicyCheckbox
        id="org-force-mfa"
        label="Require multi-factor authentication"
        checked={forceMfa}
        onChange={setForceMfa}
      />
      <PolicyCheckbox
        id="org-allow-register"
        label="Allow anyone to self-register an account"
        checked={allowRegister}
        onChange={setAllowRegister}
      />
      {!allowRegister && (
        <p className="text-sm text-muted-foreground">
          Self-registration is closed. New accounts are created by inviting them or by enrolling
          them below.
        </p>
      )}
      <div className="grid gap-2">
        <FieldLabel htmlFor="org-min-length">Minimum password length</FieldLabel>
        <Input
          id="org-min-length"
          type="number"
          min={1}
          value={minLength}
          onChange={(e) => {
            setMinLength(Number(e.target.value));
          }}
        />
      </div>
      <PasswordComplexityFields fields={complexity} />
      <Button type="submit" disabled={saving} className="justify-self-start">
        {saving ? 'Saving…' : 'Save settings'}
      </Button>
      <h3 className="mt-2 text-sm font-semibold text-foreground">Verified domains</h3>
      <DomainsList domains={settings.verifiedDomains} />
    </form>
  );
}

function OrgSettingsBody({
  state,
  onSave,
}: {
  state: OrgSettingsState;
  onSave: (input: UpdateOrgSettingsInput) => void;
}): React.ReactElement {
  if (state.status === 'loading') {
    return (
      <p className="text-sm text-muted-foreground" role="status">
        Loading org settings…
      </p>
    );
  }
  if (state.status === 'error') {
    return (
      <p className="text-sm text-destructive" role="alert">
        Could not load org settings: {state.message}
      </p>
    );
  }
  return (
    <SettingsForm settings={state.settings} saving={state.status === 'saving'} onSave={onSave} />
  );
}

// OrgSettingsPanel is the console's view of the platform IdP org settings an
// operator actually changes (issue #1209): MFA requirement, password
// complexity, and the org's verified domains (read-only). Only rendered for
// an OPERATIONS tenant — see App.tsx.
export function OrgSettingsPanel({ token }: { token: string }): React.ReactElement {
  const { state, save } = useOrgSettingsController(token);
  return (
    <Card aria-labelledby="org-settings-heading">
      <CardHeader>
        <CardTitle id="org-settings-heading">Org settings</CardTitle>
      </CardHeader>
      <CardContent>
        <OrgSettingsBody state={state} onSave={save} />
      </CardContent>
    </Card>
  );
}
