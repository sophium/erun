import type { StatusBadgeTone } from 'erun-kit';
import { StatusBadge } from 'erun-kit';
import type * as React from 'react';

import type {
  CloudContext,
  ContextStatus,
  Environment,
  EnvironmentStatus,
  Tenant,
  TenantConfigView,
} from './types';

// A pure render of the read model the parent fetched; the fetch/auth lifecycle
// lives in App. Empty collections render an empty-state line, never an empty
// table, so an empty view never reads as a disabled input.

function placeholder(value: string | undefined): string {
  return value && value.length > 0 ? value : '—';
}

function TenantHeader({ tenant }: { tenant: Tenant }): React.ReactElement {
  return (
    <header className="tenant-header">
      <h1>{tenant.name}</h1>
      <p className="tenant-meta">
        Tenant · <span>{tenant.type || 'unknown type'}</span>
      </p>
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
    <tr>
      <td>{env.name}</td>
      <td>{env.type}</td>
      <td>{placeholder(env.kubernetesContext)}</td>
      <td>{placeholder(env.runtimeVersion)}</td>
      <td>{deployedVersionCell(env)}</td>
      <td>
        {renderStatusBadge(env.status, ENV_STATUS_TONES, ENV_STATUS_LABELS)}
        {env.status === 'failed' && env.provisionError !== undefined && (
          <span className="context-error">{env.provisionError}</span>
        )}
      </td>
    </tr>
  );
}

function EnvironmentsSection({
  environments,
}: {
  environments: Environment[];
}): React.ReactElement {
  return (
    <section aria-labelledby="environments-heading">
      <h2 id="environments-heading">Environments</h2>
      {environments.length === 0 ? (
        <p className="empty-state">No environments yet.</p>
      ) : (
        <table>
          <thead>
            <tr>
              <th scope="col">Name</th>
              <th scope="col">Type</th>
              <th scope="col">Kubernetes context</th>
              <th scope="col">Runtime version</th>
              <th scope="col">Deployed version</th>
              <th scope="col">Status</th>
            </tr>
          </thead>
          <tbody>
            {environments.map((env) => (
              <EnvironmentRow key={env.environmentId} env={env} />
            ))}
          </tbody>
        </table>
      )}
    </section>
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
    <li>
      <span className="context-name">{context.name}</span>
      <span className="context-meta">
        {context.provider} · {context.region}
      </span>
      <span className="context-status">
        {renderStatusBadge(context.status, STATUS_TONES, STATUS_LABELS)}
        {context.status === 'failed' && context.provisionError !== undefined && (
          // The failure reason is essential, so it is visible inline rather
          // than hidden in a title tooltip.
          <span className="context-error">{context.provisionError}</span>
        )}
      </span>
    </li>
  );
}

function ContextsSection({ contexts }: { contexts: CloudContext[] }): React.ReactElement {
  return (
    <section aria-labelledby="contexts-heading">
      <h2 id="contexts-heading">Cloud contexts</h2>
      {contexts.length === 0 ? (
        <p className="empty-state">No cloud contexts yet.</p>
      ) : (
        <ul className="context-list">
          {contexts.map((context) => (
            <ContextItem key={context.contextId} context={context} />
          ))}
        </ul>
      )}
    </section>
  );
}

export function ConfigView({ config }: { config: TenantConfigView }): React.ReactElement {
  return (
    <div className="config-view">
      <TenantHeader tenant={config.tenant} />
      <EnvironmentsSection environments={config.environments} />
      <ContextsSection contexts={config.contexts} />
    </div>
  );
}
