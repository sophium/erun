import type { TenantConfigView } from 'erun-kit';
import * as React from 'react';

import type { OidcConfig } from '../auth/auth';
import { readTokenIdentity } from '../auth/identity';
import { ConfigView } from '../config/ConfigView';
import { EnvironmentsPanel } from '../environments/EnvironmentsPanel';
import { InvitesPanel } from '../identity/InvitesPanel';
import { OrgSettingsPanel } from '../identity/OrgSettingsPanel';
import { SmtpSettingsPanel } from '../identity/SmtpSettingsPanel';
import { UsersPanel } from '../identity/UsersPanel';
import { MCPAccessPanel } from '../mcp/MCPAccessPanel';
import { ProvisionPanel } from '../provision/ProvisionPanel';
import { TenantsPanel } from '../tenants/TenantsPanel';
import { ConsoleHeader } from './ConsoleHeader';
import { ConsoleSidebar } from './ConsoleSidebar';
import { type ConsoleSection, type ConsoleSectionId, sectionsForTenant } from './sections';
import {
  type TenantSwitchMismatch,
  TenantSwitchMismatchBanner,
} from './TenantSwitchMismatchBanner';
import { useTheme } from './useTheme';

// SectionContent renders exactly one panel for the active section — the main
// pane switches wholesale rather than stacking every panel, per #1207. Split
// out of AppShell purely to keep AppShell under the module's
// max-lines-per-function budget.
function SectionContent({
  active,
  token,
  config,
  docsUrl,
  onChanged,
}: {
  active: ConsoleSectionId;
  token: string;
  config: TenantConfigView;
  docsUrl: string | undefined;
  onChanged: () => void;
}): React.ReactElement {
  switch (active) {
    case 'overview':
      return <ConfigView config={config} />;
    case 'environments':
      return (
        <EnvironmentsPanel
          token={token}
          contexts={config.contexts}
          environments={config.environments}
          onChanged={onChanged}
        />
      );
    case 'provisioning':
      return <ProvisionPanel token={token} />;
    case 'mcp-access':
      return <MCPAccessPanel token={token} environments={config.environments} />;
    case 'invites':
      return <InvitesPanel token={token} />;
    case 'tenants':
      return <TenantsPanel token={token} docsUrl={docsUrl} />;
    case 'users':
      return <UsersPanel token={token} />;
    case 'org-settings':
      return <OrgSettingsPanel token={token} />;
    case 'smtp-settings':
      return <SmtpSettingsPanel token={token} />;
  }
}

function sectionLabel(sections: ConsoleSection[], active: ConsoleSectionId): string {
  return sections.find((section) => section.id === active)?.label ?? '';
}

// AppShell is the console's app shell (#1207): a persistent sidebar for
// navigation, a header carrying identity + sign-out, and a main pane that
// switches on a single derived `active` value rather than stacking every
// panel vertically.
export function AppShell({
  brand,
  token,
  config,
  docsUrl,
  oidc,
  switchMismatch,
  onRetrySwitch,
  onDismissSwitchMismatch,
  onChanged,
  onSignOut,
}: {
  brand: string | undefined;
  token: string;
  config: TenantConfigView;
  docsUrl?: string;
  oidc: OidcConfig | undefined;
  switchMismatch: TenantSwitchMismatch | undefined;
  onRetrySwitch?: () => void;
  onDismissSwitchMismatch: () => void;
  onChanged: () => void;
  onSignOut: () => void;
}): React.ReactElement {
  const sections = React.useMemo(() => sectionsForTenant(config.tenant), [config.tenant]);
  const [active, setActive] = React.useState<ConsoleSectionId>('overview');
  const { theme, toggleTheme } = useTheme();
  const identity = React.useMemo(() => readTokenIdentity(token), [token]);

  return (
    <div className="flex h-dvh min-h-0 w-full bg-background text-foreground">
      <ConsoleSidebar
        brand={brand}
        token={token}
        currentTenant={config.tenant}
        oidc={oidc}
        sections={sections}
        active={active}
        onSelect={setActive}
      />
      <div className="flex min-w-0 flex-1 flex-col">
        <ConsoleHeader
          title={sectionLabel(sections, active)}
          identityLabel={identity.email ?? identity.subject}
          theme={theme}
          onToggleTheme={toggleTheme}
          onSignOut={onSignOut}
        />
        {switchMismatch !== undefined && (
          <TenantSwitchMismatchBanner
            mismatch={switchMismatch}
            onRetry={onRetrySwitch}
            onDismiss={onDismissSwitchMismatch}
          />
        )}
        <main className="min-h-0 flex-1 overflow-auto px-6 py-6">
          <SectionContent
            active={active}
            token={token}
            config={config}
            docsUrl={docsUrl}
            onChanged={onChanged}
          />
        </main>
      </div>
    </div>
  );
}
