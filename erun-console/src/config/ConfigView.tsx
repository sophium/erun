import {
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  type CloudContext,
  type ContextStatus,
  EmptyState,
  type Environment,
  type EnvironmentStatus,
  StatusBadge,
  type StatusBadgeTone,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  type Tenant,
  type TenantConfigView,
} from 'erun-kit';
import { Cloud, Server } from 'lucide-react';
import type * as React from 'react';

import { useReconcileTenantNameMutation } from '../app/api/tenantsApi';
import { queryErrorMessage } from '../app/queryError';

// A pure render of the read model the parent fetched; the fetch/auth lifecycle
// lives in App. Empty collections render an empty-state card, never an empty
// table, so an empty view never reads as a disabled input.

function placeholder(value: string | undefined): string {
  return value && value.length > 0 ? value : '—';
}

// TenantNameMismatchBanner surfaces the one disagreement the backend
// otherwise leaves discoverable only by querying the database: a platform
// bootstrapped before its own tenant name was read from ERUN_TENANT still
// carries the legacy placeholder. tenant.platformDeclaredName is present
// only in that exact case, so its presence alone gates the banner.
function TenantNameMismatchBanner({
  tenant,
  token,
}: {
  tenant: Tenant;
  token: string;
}): React.ReactElement | null {
  const [reconcile, { isLoading, error }] = useReconcileTenantNameMutation();
  const declaredName = tenant.platformDeclaredName;
  if (declaredName === undefined) {
    return null;
  }
  return (
    <div
      role="alert"
      className="flex flex-wrap items-center justify-between gap-3 rounded-[calc(var(--radius)-2px)] border border-amber-500/40 bg-amber-500/10 px-4 py-3 text-sm text-amber-700 dark:text-amber-400"
    >
      <span>
        This tenant is named &quot;{tenant.name}&quot;, but this platform declares its own identity
        as &quot;{declaredName}&quot;. Renaming it lets provisioning resolve this platform&apos;s
        own published runtime image.
      </span>
      <div className="flex flex-col items-end gap-1">
        <Button
          type="button"
          variant="outline"
          disabled={isLoading}
          onClick={() => {
            void reconcile(token);
          }}
        >
          {isLoading ? 'Renaming…' : `Rename to "${declaredName}"`}
        </Button>
        {error !== undefined && (
          <span className="text-xs text-destructive">{queryErrorMessage(error)}</span>
        )}
      </div>
    </div>
  );
}

function TenantHeader({ tenant, token }: { tenant: Tenant; token: string }): React.ReactElement {
  return (
    <header className="grid gap-3">
      <div>
        <h2 className="text-xl font-semibold text-foreground">{tenant.name}</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          Tenant · {tenant.type || 'unknown type'}
        </p>
      </div>
      <TenantNameMismatchBanner tenant={tenant} token={token} />
    </header>
  );
}

// deployedVersionCell renders the version the last successful deploy actually
// installed, distinct from the declared pin (the "Runtime version" column) —
// a failed or in-flight deploy leaves this on whatever is still running.
function deployedVersionCell(env: Environment): string {
  if (env.deployedVersion === undefined) {
    return '—';
  }
  return env.deployedVersion !== env.runtimeVersion
    ? `${env.deployedVersion} (pin: ${placeholder(env.runtimeVersion)})`
    : env.deployedVersion;
}

function EnvironmentRow({ env }: { env: Environment }): React.ReactElement {
  return (
    <TableRow>
      <TableCell className="font-medium text-foreground">{env.name}</TableCell>
      <TableCell>{env.type}</TableCell>
      <TableCell>{placeholder(env.kubernetesContext)}</TableCell>
      <TableCell>{placeholder(env.runtimeVersion)}</TableCell>
      <TableCell>{deployedVersionCell(env)}</TableCell>
      <TableCell>
        <div className="flex flex-col items-start gap-1">
          {renderStatusBadge(env.status, ENV_STATUS_TONES, ENV_STATUS_LABELS)}
          {env.status === 'failed' && env.provisionError !== undefined && (
            <span className="text-xs text-destructive">{env.provisionError}</span>
          )}
        </div>
      </TableCell>
    </TableRow>
  );
}

function EnvironmentsSection({
  environments,
}: {
  environments: Environment[];
}): React.ReactElement {
  return (
    <Card role="region" aria-labelledby="environments-heading">
      <CardHeader>
        <CardTitle id="environments-heading">Environments</CardTitle>
      </CardHeader>
      <CardContent>
        {environments.length === 0 ? (
          <EmptyState icon={<Server />} heading="No environments yet." />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Kubernetes context</TableHead>
                <TableHead>Runtime version</TableHead>
                <TableHead>Deployed version</TableHead>
                <TableHead>Status</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {environments.map((env) => (
                <EnvironmentRow key={env.environmentId} env={env} />
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}

const STATUS_LABELS: Record<ContextStatus, string> = {
  provisioning: 'Provisioning',
  running: 'Running',
  failed: 'Failed',
};

const STATUS_TONES: Record<ContextStatus, StatusBadgeTone> = {
  provisioning: 'in-progress',
  running: 'success',
  failed: 'destructive',
};

const ENV_STATUS_LABELS: Record<EnvironmentStatus, string> = {
  registered: 'Registered',
  provisioning: 'Provisioning',
  running: 'Running',
  failed: 'Failed',
  // Without these two the lenient status parse rendered no badge at all for an
  // environment mid-teardown, so it read as an ordinary one (#1170).
  deleting: 'Deleting',
  'deletion-blocked': 'Delete blocked',
};

const ENV_STATUS_TONES: Record<EnvironmentStatus, StatusBadgeTone> = {
  registered: 'muted',
  provisioning: 'in-progress',
  running: 'success',
  failed: 'destructive',
  deleting: 'in-progress',
  'deletion-blocked': 'warning',
};

// Shares the desktop's StatusBadge (erun-kit) so the same erun status renders
// identically in both surfaces. A resource with no status (registered before
// provisioning existed) renders no badge. Generic over the status maps so
// contexts and environments share one call site.
function renderStatusBadge<T extends string>(
  status: T | undefined,
  tones: Record<T, StatusBadgeTone>,
  labels: Record<T, string>,
): React.ReactElement | null {
  if (status === undefined) {
    return null;
  }
  return <StatusBadge tone={tones[status]} label={labels[status]} />;
}

function ContextItem({ context }: { context: CloudContext }): React.ReactElement {
  return (
    <li className="flex items-center justify-between gap-3 border-b border-border py-2.5 text-sm last:border-b-0">
      <span className="font-medium text-foreground">{context.name}</span>
      <span className="text-muted-foreground">
        {context.provider} · {context.region}
      </span>
      <span className="flex flex-col items-end gap-1">
        {renderStatusBadge(context.status, STATUS_TONES, STATUS_LABELS)}
        {context.status === 'failed' && context.provisionError !== undefined && (
          <span className="text-xs text-destructive">{context.provisionError}</span>
        )}
      </span>
    </li>
  );
}

function ContextsSection({ contexts }: { contexts: CloudContext[] }): React.ReactElement {
  return (
    <Card role="region" aria-labelledby="contexts-heading">
      <CardHeader>
        <CardTitle id="contexts-heading">Cloud contexts</CardTitle>
      </CardHeader>
      <CardContent>
        {contexts.length === 0 ? (
          <EmptyState icon={<Cloud />} heading="No cloud contexts yet." />
        ) : (
          <ul>
            {contexts.map((context) => (
              <ContextItem key={context.contextId} context={context} />
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}

export function ConfigView({
  config,
  token,
}: {
  config: TenantConfigView;
  token: string;
}): React.ReactElement {
  return (
    <div className="grid gap-6">
      <TenantHeader tenant={config.tenant} token={token} />
      <EnvironmentsSection environments={config.environments} />
      <ContextsSection contexts={config.contexts} />
    </div>
  );
}
