import { Button, EmptyState, FieldLabel, Input, StatusBadge, TabsContent } from 'erun-kit';
import { Cloud, LoaderCircle, Plus, Server } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { registrationStatusTone, tenantDashboardPanel } from '@/app/tenantDashboardPanels';
import { createPlatformContext, updateRegistrationDraft } from '@/app/tenantRegistrationThunks';
import type { UIPlatformContext } from '@/types';

import { InlineAlert, PermissionNotice } from './InlineAlert';
import { DataCell, DataTable, type TenantDashboardData } from './TenantDashboardMessage';
import { RegistrationEnvironmentsSection } from './TenantDashboardPanels.RegistrationEnvironments';

// TenantDashboardPanels.Registration.tsx is the Registration tab: the
// registration path `erun platform` gives the CLI (context create, provision
// preview, env register/deploy/stop/delete) and no desktop surface had — the
// gap this file closes. `erun platform tenant create`/`user enroll` stay out
// of scope on purpose (see tenant_platform_registration.go's header comment);
// TwoRegistriesNotice below points there instead of half-implementing them.

export function RegistrationPanel({ data }: { data: TenantDashboardData }): React.ReactElement {
  const panel = tenantDashboardPanel(data, 'registration');
  return (
    <TabsContent value="registration" className="min-h-0 overflow-auto">
      <div className="mt-4 grid gap-6">
        <TwoRegistriesNotice tenant={data?.tenant ?? ''} />
        {panel?.restricted ? (
          <PermissionNotice>
            Registering anything on the platform needs {panel.restricted}. Ask an administrator for
            access.
          </PermissionNotice>
        ) : (
          <>
            <ContextsSection data={data} />
            <RegistrationEnvironmentsSection data={data} />
          </>
        )}
      </div>
    </TabsContent>
  );
}

// TwoRegistriesNotice is decision (4): the tenant dashboard names the two
// distinct objects an operator otherwise conflates — a tenant/env created in
// this desktop only exists in this machine's local config until it is
// registered here, and a hosted registration has no automatic link back to
// that local object even when the names match. It also names decision (2):
// creating a tenant, or enrolling the first user, stays a CLI/console action
// because it configures an OIDC issuer mapping this form has no safe way to
// guess.
function TwoRegistriesNotice({ tenant }: { tenant: string }): React.ReactElement {
  return (
    <div className="grid gap-1.5 rounded-[var(--radius)] border border-border bg-muted/30 px-3 py-2.5 text-[13px] leading-[1.4] text-muted-foreground">
      <p>
        <span className="font-medium text-foreground">{tenant || 'This tenant'}</span> and its
        environments in the sidebar live in this machine&apos;s local config. The platform keeps its
        own, separate tenant and environment rows — creating one here does not create the other. Use
        the lists below to register this tenant&apos;s hosted contexts and environments, or to see
        what is already registered.
      </p>
      <p>
        Creating a new tenant, or enrolling its first user, is a platform-configuration step (an
        OIDC issuer mapping) this form cannot safely guess — run{' '}
        <code className="rounded bg-background px-1 py-px font-mono text-xs">
          erun platform tenant create
        </code>{' '}
        from a terminal, or use the console.
      </p>
    </div>
  );
}

function ContextsTable({ contexts }: { contexts: UIPlatformContext[] }): React.ReactElement {
  return (
    <DataTable headers={['Name', 'Status', 'Provider', 'Region']}>
      {contexts.map((context) => (
        <tr key={context.contextId}>
          <DataCell strong>{context.name}</DataCell>
          <DataCell>
            <StatusBadge tone={registrationStatusTone(context.status)} label={context.status} />
          </DataCell>
          <DataCell>{context.provider}</DataCell>
          <DataCell>{context.region}</DataCell>
        </tr>
      ))}
    </DataTable>
  );
}

function ContextsList({ data }: { data: TenantDashboardData }): React.ReactElement {
  if (data?.contextsRestricted) {
    return (
      <PermissionNotice>
        Listing cloud contexts needs {data.contextsRestricted}. Ask an administrator for access.
      </PermissionNotice>
    );
  }
  if (data?.contextsError) {
    return <InlineAlert>{data.contextsError}</InlineAlert>;
  }
  const contexts = data?.contexts ?? [];
  if (contexts.length === 0) {
    return (
      <EmptyState
        icon={<Cloud />}
        heading="No cloud contexts registered"
        body="A cloud context is the managed cluster a hosted environment deploys into. Register one below."
      />
    );
  }
  return <ContextsTable contexts={contexts} />;
}

function CreateContextForm({ data }: { data: TenantDashboardData }): React.ReactElement | null {
  const dispatch = useAppDispatch();
  const draft = useAppSelector((state) => state.tenantDashboard.registration);
  if (data?.canCreateContext !== true) {
    return null;
  }
  const busy = draft.creatingContext;
  const canSubmit =
    draft.contextName.trim() !== '' &&
    draft.contextCloudProviderAlias.trim() !== '' &&
    draft.contextRegion.trim() !== '';
  return (
    <form
      className="grid max-w-md gap-3"
      onSubmit={(event) => {
        event.preventDefault();
        void dispatch(createPlatformContext(false));
      }}
    >
      <h3 className="text-sm font-medium text-foreground">Register a cloud context</h3>
      <div className="grid gap-2">
        <FieldLabel htmlFor="context-name" required>
          Name
        </FieldLabel>
        <Input
          id="context-name"
          value={draft.contextName}
          disabled={busy}
          onChange={(event) => {
            dispatch(updateRegistrationDraft({ contextName: event.target.value }));
          }}
        />
      </div>
      <div className="grid gap-2">
        <FieldLabel htmlFor="context-alias" required>
          Cloud provider alias
        </FieldLabel>
        <Input
          id="context-alias"
          placeholder="aws-main"
          value={draft.contextCloudProviderAlias}
          disabled={busy}
          onChange={(event) => {
            dispatch(updateRegistrationDraft({ contextCloudProviderAlias: event.target.value }));
          }}
        />
      </div>
      <div className="grid gap-2">
        <FieldLabel htmlFor="context-region" required>
          Region
        </FieldLabel>
        <Input
          id="context-region"
          placeholder="eu-west-2"
          value={draft.contextRegion}
          disabled={busy}
          onChange={(event) => {
            dispatch(updateRegistrationDraft({ contextRegion: event.target.value }));
          }}
        />
      </div>
      <div className="flex items-center gap-2">
        <Button
          type="button"
          disabled={busy || !canSubmit}
          onClick={() => {
            void dispatch(createPlatformContext(true));
          }}
        >
          Preview context plan
        </Button>
        <Button type="submit" variant="outline" disabled={busy || !canSubmit}>
          {busy && <LoaderCircle className="animate-spin" aria-hidden="true" />}
          <Plus aria-hidden="true" />
          {busy ? 'Registering…' : 'Register context'}
        </Button>
      </div>
      <p className="text-[13px] text-muted-foreground">
        Registering launches a real cloud VM and bills your cloud account until it is stopped.
        Preview the plan first — it creates nothing.
      </p>
      <CreateContextFeedback
        plan={draft.contextPreviewPlan}
        conflict={draft.createContextConflict}
        error={draft.createContextError}
      />
    </form>
  );
}

function CreateContextFeedback({
  plan,
  conflict,
  error,
}: {
  plan: string[] | null;
  conflict: string;
  error: string;
}): React.ReactElement {
  return (
    <>
      {plan && plan.length > 0 && <PlanList plan={plan} />}
      {conflict && (
        <p role="status" className="text-[13px] text-muted-foreground">
          {conflict}
        </p>
      )}
      {error && <InlineAlert>{error}</InlineAlert>}
    </>
  );
}

// PlanList renders a resolved plan's ordered steps verbatim — the actual
// resolved actions (root AGENTS.md: "prefer the actual commands... over
// summary notes"), not a paraphrase.
export function PlanList({ plan }: { plan: string[] }): React.ReactElement {
  return (
    <ol className="grid gap-1 rounded-[var(--radius)] border border-border bg-muted/30 px-3 py-2 text-[13px] text-muted-foreground">
      {plan.map((line, index) => (
        <li key={`${String(index)}-${line}`} className="font-mono text-xs">
          {line}
        </li>
      ))}
    </ol>
  );
}

function ContextsSection({ data }: { data: TenantDashboardData }): React.ReactElement {
  return (
    <section className="grid gap-4">
      <h2 className="flex items-center gap-2 text-[15px] font-semibold text-foreground">
        <Server className="size-4 text-muted-foreground" aria-hidden="true" />
        Cloud contexts
      </h2>
      <ContextsList data={data} />
      <CreateContextForm data={data} />
    </section>
  );
}
