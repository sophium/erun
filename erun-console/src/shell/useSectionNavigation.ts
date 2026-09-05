import type { TenantConfigView } from 'erun-kit';
import * as React from 'react';

import { type ConsoleSection, type ConsoleSectionId, sectionsForTenant } from './sections';
import { pathForSection, resolveSection } from './sectionUrl';

export interface SectionNavigation {
  sections: ConsoleSection[];
  active: ConsoleSectionId;
  onSelect: (id: ConsoleSectionId) => void;
}

// useSectionNavigation keeps the active section, the URL, and the tenant's
// permitted set in sync in both directions: selecting a nav item pushes a
// history entry so reload, a deep link, and Back/Forward all resolve to the
// same section, and any candidate -- from the URL on mount, or from a
// tenant switch that shrinks the permitted set -- is revalidated against
// `sectionsForTenant` before it renders, falling back to Overview rather than
// a panel the nav itself would never have offered.
export function useSectionNavigation(tenant: TenantConfigView['tenant']): SectionNavigation {
  const sections = React.useMemo(() => sectionsForTenant(tenant), [tenant]);
  const [active, setActive] = React.useState<ConsoleSectionId>(() =>
    resolveSection(window.location.pathname, sectionsForTenant(tenant)),
  );

  // Revalidates on mount (correcting a deep link or bookmark whose section
  // the current tenant may not permit) and whenever the permitted set itself
  // changes under an already-rendered section. Corrects the URL and the
  // rendered section together -- never just one of them -- so the address
  // bar never disagrees with what is on screen.
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

  // Back/Forward moves the URL without going through onSelect, so it needs
  // its own listener -- the effect above only reacts to sections/active
  // changing, not to the browser's own history navigation.
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
