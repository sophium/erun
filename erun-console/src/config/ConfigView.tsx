import type * as React from 'react';

import type { CloudContext, ContextStatus, Environment, Tenant, TenantConfigView } from './types';

// What an Operator sees in the console: the tenant header, the list of
// environments (name, type, kubernetes context, runtime version), and the list
// of cloud contexts (name, provider, region). Empty collections render a plain
// empty-state line rather than an empty table — an empty state must not look
// like a disabled input (Material "Empty states"; erun-ui/AGENTS.md § Professional
// UX). This is a pure render of the read model the parent already fetched; the
// fetch/auth lifecycle lives in App.

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

// A semantic, non-color-only status badge: it always carries a text label
// (Provisioning / Running / Failed) alongside the color, so it reads correctly
// for color-blind users and screen readers (jsx-a11y; erun-ui/AGENTS.md
// § Professional UX). Returns null for an absent/unknown status so a context
// registered before provisioning existed renders no badge.
function StatusBadge({ status }: { status: ContextStatus | undefined }): React.ReactElement | null {
  if (status === undefined) {
    return null;
  }
  return <span className={`status-badge status-badge--${status}`}>{STATUS_LABELS[status]}</span>;
}

function ContextItem({ context }: { context: CloudContext }): React.ReactElement {
  return (
    <li>
      <span className="context-name">{context.name}</span>
      <span className="context-meta">
        {context.provider} · {context.region}
      </span>
      <span className="context-status">
        <StatusBadge status={context.status} />
        {context.status === 'failed' && context.provisionError !== undefined && (
          // The failure reason is essential information, so it is shown inline
          // as visible text rather than hidden behind a bare `title` tooltip
          // (jsx-a11y / module a11y rules).
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
