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

function EnvironmentRow({ env }: { env: Environment }): React.ReactElement {
  return (
    <tr>
      <td>{env.name}</td>
      <td>{env.type}</td>
      <td>{placeholder(env.kubernetesContext)}</td>
      <td>{placeholder(env.runtimeVersion)}</td>
      <td>
        <StatusBadge status={env.status} labels={ENV_STATUS_LABELS} />
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

const ENV_STATUS_LABELS: Record<EnvironmentStatus, string> = {
  registered: 'Registered',
  provisioning: 'Provisioning',
  running: 'Running',
  failed: 'Failed',
};

// The badge pairs color with a text label so it reads for color-blind and
// screen-reader users, not color alone. A resource with no status (registered
// before provisioning existed) renders no badge. Generic over the status label
// map so contexts and environments share one badge.
function StatusBadge<T extends string>({
  status,
  labels,
}: {
  status: T | undefined;
  labels: Record<T, string>;
}): React.ReactElement | null {
  if (status === undefined) {
    return null;
  }
  return <span className={`status-badge status-badge--${status}`}>{labels[status]}</span>;
}

function ContextItem({ context }: { context: CloudContext }): React.ReactElement {
  return (
    <li>
      <span className="context-name">{context.name}</span>
      <span className="context-meta">
        {context.provider} · {context.region}
      </span>
      <span className="context-status">
        <StatusBadge status={context.status} labels={STATUS_LABELS} />
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
