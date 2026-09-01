import {
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  EmptyState,
  FieldLabel,
  Input,
  SelectField,
  StatusBadge,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from 'erun-kit';
import { Building2 } from 'lucide-react';
import * as React from 'react';

import type { CreateTenantInput, PlatformTenant } from '../app/api/tenantsApi';
import { PUBLIC_DOCS_URL } from '../shell/landingContent';
import type { CreateTenantState, TenantFieldError, TenantsState } from './controller';
import { useTenantsController } from './controller';
import { EnrollTenantUserDialog } from './EnrollTenantUserDialog';
import { TenantQuotaDialog } from './TenantQuotaDialog';

// The identity model's own explanation of org-scoped (shared) issuers — see
// erun-docs/docs/agent-reference/api-protocol.md#tenant-issuers — so
// orgFieldKey/orgFieldValue isn't guesswork.
const ORG_SCOPED_ISSUER_DOCS_PATH = '/agent-reference/api-protocol#tenant-issuers';

const TENANT_TYPES = ['COMPANY', 'OPERATIONS'];

function fieldError(createState: CreateTenantState, field: TenantFieldError): string | undefined {
  return createState.status === 'error' && createState.field === field
    ? createState.message
    : undefined;
}

function FieldError({ message }: { message: string | undefined }): React.ReactElement | null {
  if (message === undefined) {
    return null;
  }
  return (
    <p className="text-sm text-destructive" role="alert">
      {message}
    </p>
  );
}

// CreateFeedback carries only the failures that aren't pinned to one field
// (a malformed body, the operations-only refusal) — the field-scoped ones
// render inline next to the control they're about instead.
function CreateFeedback({
  createState,
}: {
  createState: CreateTenantState;
}): React.ReactElement | null {
  if (createState.status === 'created') {
    return (
      <p className="text-sm text-muted-foreground" role="status">
        Registered {createState.tenant.name}. No first user is created here — the tenant&apos;s
        first admin is whoever presents the first valid token that resolves to it.
      </p>
    );
  }
  if (createState.status === 'error' && createState.field === undefined) {
    return (
      <p className="text-sm text-destructive" role="alert">
        Could not register tenant: {createState.message}
      </p>
    );
  }
  return null;
}

// useCreateTenantFormValues holds every field of the create-tenant form as
// one unit, so CreateTenantForm only has one hook call to account for
// instead of six repeated pairs (mirrors OrgSettingsPanel's
// usePasswordComplexityFields).
function useCreateTenantFormValues(): {
  name: string;
  setName: (value: string) => void;
  type: string;
  setType: (value: string) => void;
  issuer: string;
  setIssuer: (value: string) => void;
  orgFieldKey: string;
  setOrgFieldKey: (value: string) => void;
  orgFieldValue: string;
  setOrgFieldValue: (value: string) => void;
  displayName: string;
  setDisplayName: (value: string) => void;
} {
  const [name, setName] = React.useState('');
  const [type, setType] = React.useState('COMPANY');
  const [issuer, setIssuer] = React.useState('');
  const [orgFieldKey, setOrgFieldKey] = React.useState('');
  const [orgFieldValue, setOrgFieldValue] = React.useState('');
  const [displayName, setDisplayName] = React.useState('');
  return {
    name,
    setName,
    type,
    setType,
    issuer,
    setIssuer,
    orgFieldKey,
    setOrgFieldKey,
    orgFieldValue,
    setOrgFieldValue,
    displayName,
    setDisplayName,
  };
}

function NameAndTypeFields({
  values,
  createState,
}: {
  values: ReturnType<typeof useCreateTenantFormValues>;
  createState: CreateTenantState;
}): React.ReactElement {
  return (
    <>
      <div className="grid gap-2">
        <FieldLabel htmlFor="tenant-name" required>
          Name
        </FieldLabel>
        <Input
          id="tenant-name"
          value={values.name}
          onChange={(e) => {
            values.setName(e.target.value);
          }}
          required
        />
        <p className="text-xs text-muted-foreground">
          Lowercase letters and digits only — no hyphens.
        </p>
        <FieldError message={fieldError(createState, 'name')} />
      </div>
      <SelectField
        id="tenant-type"
        label="Type"
        value={values.type}
        options={TENANT_TYPES.map((option) => ({ value: option, label: option }))}
        onChange={values.setType}
      />
      <FieldError message={fieldError(createState, 'type')} />
    </>
  );
}

// OrgScopedIssuerFields carries the issuer and its optional org-scoping pair
// together, since the org-scoped-issuer explanation link right below them is
// about how the two relate.
function OrgScopedIssuerFields({
  values,
  createState,
  docsUrl,
}: {
  values: ReturnType<typeof useCreateTenantFormValues>;
  createState: CreateTenantState;
  docsUrl: string | undefined;
}): React.ReactElement {
  const orgScopedIssuerHref = `${docsUrl !== undefined && docsUrl.length > 0 ? docsUrl : PUBLIC_DOCS_URL}${ORG_SCOPED_ISSUER_DOCS_PATH}`;
  return (
    <>
      <div className="grid gap-2">
        <FieldLabel htmlFor="tenant-issuer" required>
          Issuer
        </FieldLabel>
        <Input
          id="tenant-issuer"
          value={values.issuer}
          onChange={(e) => {
            values.setIssuer(e.target.value);
          }}
          placeholder="https://idp.example.com"
          required
        />
        <FieldError message={fieldError(createState, 'issuer')} />
      </div>
      <div className="grid gap-2">
        <FieldLabel htmlFor="tenant-org-field-key">Org field key (optional)</FieldLabel>
        <Input
          id="tenant-org-field-key"
          value={values.orgFieldKey}
          onChange={(e) => {
            values.setOrgFieldKey(e.target.value);
          }}
          placeholder="org_id"
        />
      </div>
      <div className="grid gap-2">
        <FieldLabel htmlFor="tenant-org-field-value">Org field value (optional)</FieldLabel>
        <Input
          id="tenant-org-field-value"
          value={values.orgFieldValue}
          onChange={(e) => {
            values.setOrgFieldValue(e.target.value);
          }}
          placeholder="42"
        />
      </div>
      <p className="text-xs text-muted-foreground">
        Only needed when this issuer is shared across tenants — see{' '}
        <a
          href={orgScopedIssuerHref}
          target="_blank"
          rel="noreferrer"
          className="font-medium underline underline-offset-4"
        >
          how tenant resolution works
        </a>
        .
      </p>
    </>
  );
}

function CreateTenantForm({
  createState,
  docsUrl,
  onCreate,
}: {
  createState: CreateTenantState;
  docsUrl: string | undefined;
  onCreate: (input: CreateTenantInput) => void;
}): React.ReactElement {
  const values = useCreateTenantFormValues();
  const busy = createState.status === 'creating';

  const submit = (event: React.SyntheticEvent): void => {
    event.preventDefault();
    onCreate({
      name: values.name.trim(),
      type: values.type,
      issuer: values.issuer.trim(),
      orgFieldKey: values.orgFieldKey.trim() || undefined,
      orgFieldValue: values.orgFieldValue.trim() || undefined,
      displayName: values.displayName.trim() || undefined,
    });
  };

  return (
    <form className="grid max-w-md gap-3" onSubmit={submit} aria-labelledby="create-tenant-heading">
      <h3 id="create-tenant-heading" className="text-sm font-semibold text-foreground">
        Register a tenant
      </h3>
      <NameAndTypeFields values={values} createState={createState} />
      <OrgScopedIssuerFields values={values} createState={createState} docsUrl={docsUrl} />
      <div className="grid gap-2">
        <FieldLabel htmlFor="tenant-display-name">Display name (optional)</FieldLabel>
        <Input
          id="tenant-display-name"
          value={values.displayName}
          onChange={(e) => {
            values.setDisplayName(e.target.value);
          }}
          placeholder="Defaults to the issuer URL"
        />
      </div>
      <Button type="submit" disabled={busy} className="justify-self-start">
        {busy ? 'Registering…' : 'Register tenant'}
      </Button>
      <CreateFeedback createState={createState} />
    </form>
  );
}

function formatCreatedAt(createdAt: string): string {
  const parsed = new Date(createdAt);
  return Number.isNaN(parsed.getTime()) ? createdAt : parsed.toLocaleDateString();
}

// TenantUserCountBadge is the Tenants view's own noticing point for an inert
// tenant (erun#1744): a tenant registered through the product but with no
// console-reachable way to add a user reads identically to a healthy one
// unless the zero itself is flagged here, where it was created. `undefined`
// (the count did not load, rather than genuinely being zero) renders as
// "Unknown" so a fetch hiccup never gets mistaken for the inert state.
function TenantUserCountBadge({
  userCount,
}: {
  userCount: number | undefined;
}): React.ReactElement {
  if (userCount === undefined) {
    return <StatusBadge tone="muted" label="Unknown" showIcon={false} />;
  }
  if (userCount === 0) {
    return <StatusBadge tone="warning" label="No users" />;
  }
  return (
    <StatusBadge
      tone="muted"
      label={`${String(userCount)} ${userCount === 1 ? 'user' : 'users'}`}
      showIcon={false}
    />
  );
}

// TenantSignInBadge is where a tenant nobody can ever sign in to stops being
// invisible: a tenant whose issuer mapping contradicts its issuer's
// org-scoping mode exists, lists, and accepts enrollments while no token can
// resolve to it. `POST /v1/tenants` refuses to create that shape now, so this
// is for the rows a platform already carries. `undefined` (the read did not
// compute it) renders as "Unknown" rather than as working, for the same
// reason the user count does.
function TenantSignInBadge({
  resolvable,
}: {
  resolvable: boolean | undefined;
}): React.ReactElement {
  if (resolvable === undefined) {
    return <StatusBadge tone="muted" label="Unknown" showIcon={false} />;
  }
  if (!resolvable) {
    return <StatusBadge tone="destructive" label="No working issuer mapping" />;
  }
  return <StatusBadge tone="success" label="Reachable" showIcon={false} />;
}

function TenantRow({
  tenant,
  onManageQuota,
  onEnrollUser,
}: {
  tenant: PlatformTenant;
  onManageQuota: (tenant: PlatformTenant) => void;
  onEnrollUser: (tenant: PlatformTenant) => void;
}): React.ReactElement {
  return (
    <TableRow>
      <TableCell className="font-medium text-foreground">{tenant.name}</TableCell>
      <TableCell>{tenant.type}</TableCell>
      <TableCell>{formatCreatedAt(tenant.createdAt)}</TableCell>
      <TableCell>
        <TenantUserCountBadge userCount={tenant.userCount} />
      </TableCell>
      <TableCell>
        <TenantSignInBadge resolvable={tenant.resolvable} />
      </TableCell>
      <TableCell className="flex gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => {
            onEnrollUser(tenant);
          }}
        >
          Enroll user
        </Button>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => {
            onManageQuota(tenant);
          }}
        >
          Set quota
        </Button>
      </TableCell>
    </TableRow>
  );
}

function TenantsTable({
  tenants,
  onManageQuota,
  onEnrollUser,
}: {
  tenants: PlatformTenant[];
  onManageQuota: (tenant: PlatformTenant) => void;
  onEnrollUser: (tenant: PlatformTenant) => void;
}): React.ReactElement {
  if (tenants.length === 0) {
    return <EmptyState icon={<Building2 />} heading="No tenants registered yet." />;
  }
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
          <TableHead>Type</TableHead>
          <TableHead>Created</TableHead>
          <TableHead>Users</TableHead>
          <TableHead>Sign-in</TableHead>
          <TableHead />
        </TableRow>
      </TableHeader>
      <TableBody>
        {tenants.map((tenant) => (
          <TenantRow
            key={tenant.tenantId}
            tenant={tenant}
            onManageQuota={onManageQuota}
            onEnrollUser={onEnrollUser}
          />
        ))}
      </TableBody>
    </Table>
  );
}

function TenantsBody({
  tenantsState,
  onManageQuota,
  onEnrollUser,
}: {
  tenantsState: TenantsState;
  onManageQuota: (tenant: PlatformTenant) => void;
  onEnrollUser: (tenant: PlatformTenant) => void;
}): React.ReactElement {
  if (tenantsState.status === 'loading') {
    return (
      <p className="text-sm text-muted-foreground" role="status">
        Loading tenants…
      </p>
    );
  }
  if (tenantsState.status === 'error') {
    return (
      <p className="text-sm text-destructive" role="alert">
        Could not load tenants: {tenantsState.message}
      </p>
    );
  }
  return (
    <TenantsTable
      tenants={tenantsState.tenants}
      onManageQuota={onManageQuota}
      onEnrollUser={onEnrollUser}
    />
  );
}

// TenantsPanel is the console's tenant-registration surface: the one action
// only an OPERATIONS tenant can take, mirroring Users/Org settings. Only
// rendered for an OPERATIONS tenant — see shell/sections.ts.
export function TenantsPanel({
  token,
  docsUrl,
}: {
  token: string;
  docsUrl: string | undefined;
}): React.ReactElement {
  const { tenantsState, createState, create } = useTenantsController(token);
  const [managingQuotaFor, setManagingQuotaFor] = React.useState<PlatformTenant | undefined>(
    undefined,
  );
  const [enrollingUserFor, setEnrollingUserFor] = React.useState<PlatformTenant | undefined>(
    undefined,
  );
  return (
    <Card aria-labelledby="tenants-heading">
      <CardHeader>
        <CardTitle id="tenants-heading">Tenants</CardTitle>
      </CardHeader>
      <CardContent className="grid gap-6">
        <TenantsBody
          tenantsState={tenantsState}
          onManageQuota={setManagingQuotaFor}
          onEnrollUser={setEnrollingUserFor}
        />
        <CreateTenantForm createState={createState} docsUrl={docsUrl} onCreate={create} />
      </CardContent>
      {managingQuotaFor !== undefined && (
        <TenantQuotaDialog
          tenantId={managingQuotaFor.tenantId}
          tenantName={managingQuotaFor.name}
          token={token}
          onClose={() => {
            setManagingQuotaFor(undefined);
          }}
        />
      )}
      {enrollingUserFor !== undefined && (
        <EnrollTenantUserDialog
          tenant={enrollingUserFor}
          token={token}
          onClose={() => {
            setEnrollingUserFor(undefined);
          }}
        />
      )}
    </Card>
  );
}
