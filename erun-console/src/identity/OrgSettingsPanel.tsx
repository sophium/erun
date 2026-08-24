import * as React from 'react';

import type { OrgSettings, UpdateOrgSettingsInput } from './client';
import type { OrgSettingsState } from './controller';
import { useOrgSettingsController } from './controller';

function DomainsList({ domains }: { domains: string[] }): React.ReactElement {
  if (domains.length === 0) {
    return <p className="identity-empty">No verified domains.</p>;
  }
  return (
    <ul className="identity-domains-list">
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
    <label htmlFor={id}>
      <input
        id={id}
        type="checkbox"
        checked={checked}
        onChange={(e) => {
          onChange(e.target.checked);
        }}
      />
      {label}
    </label>
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
  const [minLength, setMinLength] = React.useState(settings.minPasswordLength);
  const complexity = usePasswordComplexityFields(settings);

  const submit = (event: React.SyntheticEvent): void => {
    event.preventDefault();
    onSave({
      forceMfa,
      minPasswordLength: minLength,
      passwordRequiresUppercase: complexity.uppercase,
      passwordRequiresLowercase: complexity.lowercase,
      passwordRequiresNumber: complexity.number,
      passwordRequiresSymbol: complexity.symbol,
    });
  };

  return (
    <form className="identity-form" onSubmit={submit} aria-labelledby="org-settings-form-heading">
      <h3 id="org-settings-form-heading">Login and password policy</h3>
      <PolicyCheckbox
        id="org-force-mfa"
        label="Require multi-factor authentication"
        checked={forceMfa}
        onChange={setForceMfa}
      />
      <label htmlFor="org-min-length">Minimum password length</label>
      <input
        id="org-min-length"
        type="number"
        min={1}
        value={minLength}
        onChange={(e) => {
          setMinLength(Number(e.target.value));
        }}
      />
      <PasswordComplexityFields fields={complexity} />
      <button type="submit" disabled={saving}>
        {saving ? 'Saving…' : 'Save settings'}
      </button>
      <h3>Verified domains</h3>
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
    return <p role="status">Loading org settings…</p>;
  }
  if (state.status === 'error') {
    return (
      <p className="identity-feedback identity-feedback--error" role="alert">
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
    <section className="identity-org-settings-panel" aria-labelledby="org-settings-heading">
      <h2 id="org-settings-heading">Org settings</h2>
      <OrgSettingsBody state={state} onSave={save} />
    </section>
  );
}
