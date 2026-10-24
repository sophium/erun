import type { TenantConfigView } from 'erun-kit';
import * as React from 'react';

import { type ConsoleSection, type ConsoleSectionId, sectionsForTenant } from './sections';
import { pathForSection, resolveSection } from './sectionUrl';

export interface SectionNavigation {
  sections: ConsoleSection[];
  active: ConsoleSectionId;
  onSelect: (id: ConsoleSectionId) => void;
}

// useSectionNavigation syncs the active section with the URL, revalidating
// against the tenant's permitted sections.
export function useSectionNavigation(tenant: TenantConfigView['tenant']): SectionNavigation {
  const sections = React.useMemo(() => sectionsForTenant(tenant), [tenant]);
  const [active, setActive] = React.useState<ConsoleSectionId>(() =>
    resolveSection(window.location.pathname, sectionsForTenant(tenant)),
  );

  // Correct URL and rendered section together so they never disagree.
  React.useEffect(() => {
    const resolved = resolveSection(window.location.pathname, sections);
    const expectedPath = pathForSection(resolved);
    if (window.location.pathname !== expectedPath) {
      window.history.replaceState(null, '', expectedPath);
    }
    if (resolved !== active) {
      setActive(resolved);
    }
  }, [sections, active]);

  // Back/Forward bypasses onSelect.
  React.useEffect(() => {
    const onPopState = (): void => {
      setActive(resolveSection(window.location.pathname, sections));
    };
    window.addEventListener('popstate', onPopState);
    return () => {
      window.removeEventListener('popstate', onPopState);
    };
  }, [sections]);

  const onSelect = React.useCallback(
    (id: ConsoleSectionId) => {
      if (id !== active) {
        window.history.pushState(null, '', pathForSection(id));
      }
      setActive(id);
    },
    [active],
  );

  return { sections, active, onSelect };
}
