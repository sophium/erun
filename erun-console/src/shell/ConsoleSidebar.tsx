import { cn } from 'erun-kit';
import type * as React from 'react';

import type { OidcConfig } from '../auth/auth';
import { BrandMark } from './BrandMark';
import type { ConsoleSection, ConsoleSectionId } from './sections';
import { type CurrentTenant, TenantSwitcher } from './TenantSwitcher';

// NavBadge is the nav-level pending-count pill: a pending count must be
// visible without opening the view. It is console-shell-owned, not a kit
// widget -- a nav badge has no caller outside this sidebar, and erun-kit's
// AGENTS.md reserves new shared components for a second real consumer, not
// a speculative one.
function NavBadge({ count }: { count: number }): React.ReactElement {
  return (
    <span
      className="inline-flex min-w-5 flex-none items-center justify-center rounded-full bg-primary px-1.5 text-xs font-medium text-primary-foreground"
      aria-label={`${String(count)} pending`}
    >
      {count}
    </span>
  );
}

function NavItem({
  section,
  active,
  count,
  onSelect,
}: {
  section: ConsoleSection;
  active: boolean;
  count: number | undefined;
  onSelect: (id: ConsoleSectionId) => void;
}): React.ReactElement {
  const Icon = section.icon;
  return (
    <li>
      <button
        type="button"
        aria-current={active ? 'page' : undefined}
        className={cn(
          'flex w-full items-center gap-2.5 rounded-md px-3 py-2 text-sm font-medium transition-colors',
          active
            ? 'bg-sidebar-accent text-sidebar-accent-foreground'
            : 'text-sidebar-foreground/80 hover:bg-sidebar-accent/60 hover:text-sidebar-accent-foreground',
        )}
        onClick={() => {
          onSelect(section.id);
        }}
      >
        <Icon aria-hidden="true" className="size-4 flex-none" />
        <span className="flex-1 truncate text-left">{section.label}</span>
        {count !== undefined && count > 0 && <NavBadge count={count} />}
      </button>
    </li>
  );
}

// ConsoleSidebar is the persistent navigation: a single `active` section drives
// which item is marked current, so exactly one is ever selected at once — the
// desktop's #1204 selection defect (state derived from more than one field)
// has no equivalent here because there is only one field to derive it from.
export function ConsoleSidebar({
  brand,
  token,
  currentTenant,
  oidc,
  sections,
  active,
  counts,
  onSelect,
}: {
  brand: string | undefined;
  token: string;
  currentTenant: CurrentTenant;
  oidc: OidcConfig | undefined;
  sections: ConsoleSection[];
  active: ConsoleSectionId;
  counts: Partial<Record<ConsoleSectionId, number>>;
  onSelect: (id: ConsoleSectionId) => void;
}): React.ReactElement {
  return (
    <aside className="flex w-60 flex-none flex-col gap-4 border-r border-sidebar-border bg-sidebar px-3 py-4">
      <div className="flex items-center gap-2 px-1">
        <BrandMark brand={brand} />
        <span className="truncate text-sm font-semibold text-sidebar-foreground">
          {brand && brand.length > 0 ? brand : 'ERun console'}
        </span>
      </div>
      <TenantSwitcher token={token} current={currentTenant} oidc={oidc} />
      <nav aria-label="Console sections">
        <ul className="grid gap-0.5">
          {sections.map((section) => (
            <NavItem
              key={section.id}
              section={section}
              active={section.id === active}
              count={counts[section.id]}
              onSelect={onSelect}
            />
          ))}
        </ul>
      </nav>
    </aside>
  );
}
