import { Button, EmptyState, Input, StatusBadge } from 'erun-kit';
import { LoaderCircle, Rocket, Server, Square, Trash2 } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { registrationStatusTone } from '@/app/tenantDashboardPanels';
import type { RegistrationState } from '@/app/tenantRegistrationState';
import {
  cancelDeleteConfirmation,
  confirmDeletePlatformEnvironment,
  deletePlatformEnvironment,
  deployPlatformEnvironment,
  prefillEnvironmentFromLocal,
  stopPlatformEnvironment,
  updateRegistrationDraft,
} from '@/app/tenantRegistrationThunks';
import type { UIPlatformEnvironment } from '@/types';

import { InlineAlert, PermissionNotice } from './InlineAlert';
import { DataCell, DataTable, type TenantDashboardData } from './TenantDashboardMessage';
import { EnvironmentSection } from './TenantDashboardPanels.RegistrationForms';

function EnvironmentActionFeedback({
  environmentId,
  draft,
}: {
  environmentId: string;
  draft: RegistrationState;
}): React.ReactElement | null {
  const state = draft.envActions[environmentId];
  if (!state) {
    return null;
  }
  if (state.conflictMessage || state.unavailableMessage) {
    return (
      <p role="status" className="text-[13px] text-muted-foreground">
        {state.conflictMessage || state.unavailableMessage}
      </p>
    );
  }
  if (state.error) {
    return <InlineAlert>{state.error}</InlineAlert>;
  }
  return null;
}

function DeployControl({
  environment,
  draft,
}: {
  environment: UIPlatformEnvironment;
  draft: RegistrationState;
}): React.ReactElement | null {
  const dispatch = useAppDispatch();
  if (environment.type !== 'runtime') {
    return null;
  }
  const busy = draft.envActions[environment.environmentId]?.action === 'deploy';
  const version = draft.deployVersionDrafts[environment.environmentId] ?? '';
  return (
    <div className="flex items-center gap-1.5">
      <Input
        aria-label={`Version to deploy for ${environment.name}`}
        placeholder={environment.runtimeVersion ?? 'pinned version'}
        value={version}
        disabled={busy}
        className="h-8 w-28"
        onChange={(event) => {
          dispatch(
            updateRegistrationDraft({
              deployVersionDrafts: {
                ...draft.deployVersionDrafts,
                [environment.environmentId]: event.target.value,
              },
            }),
          );
        }}
      />
      <Button
        type="button"
        size="sm"
        variant="outline"
        disabled={busy}
        onClick={() => {
          void dispatch(deployPlatformEnvironment(environment.environmentId));
        }}
      >
        {busy ? (
          <LoaderCircle className="animate-spin" aria-hidden="true" />
        ) : (
          <Rocket aria-hidden="true" />
        )}
        Deploy
      </Button>
    </div>
  );
}

function DeleteControl({
  environment,
  draft,
}: {
  environment: UIPlatformEnvironment;
  draft: RegistrationState;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const busy = draft.envActions[environment.environmentId]?.action === 'delete';
  if (draft.deleteConfirmingEnvironmentId === environment.environmentId) {
    const confirmed = draft.deleteConfirmationDraft.trim() === environment.name;
    return (
      <div className="flex items-center gap-1.5">
        <Input
          aria-label={`Type ${environment.name} to confirm delete`}
          placeholder={environment.name}
          value={draft.deleteConfirmationDraft}
          disabled={busy}
          className="h-8 w-32"
          onChange={(event) => {
            dispatch(updateRegistrationDraft({ deleteConfirmationDraft: event.target.value }));
          }}
        />
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={busy}
          onClick={() => {
            dispatch(cancelDeleteConfirmation());
          }}
        >
          Cancel
        </Button>
        <Button
          type="button"
          size="sm"
          variant="destructive"
          disabled={busy || !confirmed}
          onClick={() => {
            void dispatch(deletePlatformEnvironment(environment.environmentId));
          }}
        >
          {busy && <LoaderCircle className="animate-spin" aria-hidden="true" />}
          Delete
        </Button>
      </div>
    );
  }
  return (
    <Button
      type="button"
      size="sm"
      variant="outline"
      disabled={busy}
      onClick={() => {
        dispatch(confirmDeletePlatformEnvironment(environment.environmentId));
      }}
    >
      <Trash2 aria-hidden="true" />
      Delete
    </Button>
  );
}

function EnvironmentRowActions({
  data,
  environment,
  draft,
}: {
  data: TenantDashboardData;
  environment: UIPlatformEnvironment;
  draft: RegistrationState;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const stopBusy = draft.envActions[environment.environmentId]?.action === 'stop';
  return (
    <div className="grid gap-1.5">
      <div className="flex flex-wrap items-center gap-1.5">
        {data?.canDeployEnvironment === true && (
          <DeployControl environment={environment} draft={draft} />
        )}
        {data?.canStopEnvironment === true && environment.type === 'runtime' && (
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={stopBusy}
            onClick={() => {
              void dispatch(stopPlatformEnvironment(environment.environmentId));
            }}
          >
            {stopBusy ? (
              <LoaderCircle className="animate-spin" aria-hidden="true" />
            ) : (
              <Square aria-hidden="true" />
            )}
            Stop
          </Button>
        )}
        {data?.canDeleteEnvironment === true && (
          <DeleteControl environment={environment} draft={draft} />
        )}
      </div>
      <EnvironmentActionFeedback environmentId={environment.environmentId} draft={draft} />
    </div>
  );
}

function EnvironmentsTable({
  data,
  environments,
  draft,
}: {
  data: TenantDashboardData;
  environments: UIPlatformEnvironment[];
  draft: RegistrationState;
}): React.ReactElement {
  return (
    <DataTable headers={['Name', 'Type', 'Status', 'Version', 'Actions']}>
      {environments.map((environment) => (
        <tr key={environment.environmentId}>
          <DataCell strong>{environment.name}</DataCell>
          <DataCell>{environment.type}</DataCell>
          <DataCell>
            <StatusBadge
              tone={registrationStatusTone(environment.status)}
              label={environment.status}
            />
            {/* Both reasons arrive after the fact: the operator provisions or
                deletes, the badge flips, and the text materialises beside it.
                Without a live region nothing is announced, so a screen-reader
                user learns the row failed only by re-reading the table — and
                this text is the whole payload of a failed or blocked row, not
                decoration.

                The two take different roles, the split the console's
                `deployFeedbackRole` already draws: `failed` is a fault, so it
                is announced as it appears (alert); `deletion-blocked` is a
                standing state the badge beside it already shows, so it does
                not interrupt (status). Each guard names its own state, so the
                role and the guard cannot disagree. */}
            {environment.status === 'deletion-blocked' && environment.deleteError && (
              <p className="mt-1 text-xs text-destructive" role="status" aria-live="polite">
                {environment.deleteError}
              </p>
            )}
            {environment.status === 'failed' && environment.provisionError && (
              <p className="mt-1 text-xs text-destructive" role="alert" aria-live="polite">
                {environment.provisionError}
              </p>
            )}
          </DataCell>
          <DataCell>{environment.deployedVersion ?? environment.runtimeVersion}</DataCell>
          <DataCell>
            <EnvironmentRowActions data={data} environment={environment} draft={draft} />
          </DataCell>
        </tr>
      ))}
    </DataTable>
  );
}

function EnvironmentsList({
  data,
  draft,
}: {
  data: TenantDashboardData;
  draft: RegistrationState;
}): React.ReactElement {
  if (data?.environmentsRestricted) {
    return (
      <PermissionNotice>
        Listing hosted environments needs {data.environmentsRestricted}. Ask an administrator for
        access.
      </PermissionNotice>
    );
  }
  if (data?.environmentsError) {
    return <InlineAlert>{data.environmentsError}</InlineAlert>;
  }
  const environments = data?.environments ?? [];
  if (environments.length === 0) {
    // Only the caller who may register gets the form above, so this copy may
    // name it only for them. EnvironmentSection renders a permission notice in
    // its place otherwise, and the empty state points at that instead of at
    // controls that are not on screen.
    return (
      <EmptyState
        icon={<Server />}
        heading="No hosted environments registered"
        body={
          data?.canRegisterEnvironment === true
            ? 'Preview a plan above, then register an environment to give it a hosted counterpart.'
            : 'Registering one needs additional access — the notice above says what is missing.'
        }
      />
    );
  }
  return <EnvironmentsTable data={data} environments={environments} draft={draft} />;
}

// LocalEnvironmentsSection is the register-on-the-row affordance: the
// desktop already knows this tenant's local environments (name, type,
// kubernetes context — from this machine's config, TwoRegistriesNotice's
// "local" registry), so putting one on the platform starts from those
// values instead of a blank form. It never asserts a name match against the
// platform's own environments list as "already registered" — no linkage
// between the two registries exists yet, so a coincidental name match would
// be a guess, not a fact.
function LocalEnvironmentsSection({
  data,
}: {
  data: TenantDashboardData;
}): React.ReactElement | null {
  const dispatch = useAppDispatch();
  const localEnvironments = useAppSelector(
    (state) =>
      state.tenants.tenants.find((tenant) => tenant.name === data?.tenant)?.environments ?? [],
  );
  if (data?.canRegisterEnvironment !== true || localEnvironments.length === 0) {
    return null;
  }
  return (
    <section className="grid gap-2">
      <h3 className="text-sm font-medium text-foreground">Your local environments</h3>
      <p className="text-[13px] text-muted-foreground">
        Put an environment you already run onto the platform without provisioning or deploying
        anything.
      </p>
      <ul className="grid gap-1.5">
        {localEnvironments.map((environment) => (
          <li
            key={environment.name}
            className="flex items-center justify-between gap-2 rounded-[var(--radius)] border border-border px-3 py-2 text-[13px]"
          >
            <span>
              <span className="font-medium text-foreground">{environment.name}</span>{' '}
              <span className="text-muted-foreground">({environment.type ?? 'local-agent'})</span>
            </span>
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() => {
                dispatch(prefillEnvironmentFromLocal(environment));
              }}
            >
              Put on platform
            </Button>
          </li>
        ))}
      </ul>
    </section>
  );
}

export function RegistrationEnvironmentsSection({
  data,
}: {
  data: TenantDashboardData;
}): React.ReactElement {
  const draft = useAppSelector((state) => state.tenantDashboard.registration);
  return (
    <section className="grid gap-4">
      <h2 className="flex items-center gap-2 text-[15px] font-semibold text-foreground">
        <Server className="size-4 text-muted-foreground" aria-hidden="true" />
        Hosted environments
      </h2>
      <LocalEnvironmentsSection data={data} />
      <EnvironmentSection data={data} draft={draft} />
      <EnvironmentsList data={data} draft={draft} />
    </section>
  );
}
