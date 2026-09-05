import { ALL_SECTION_IDS, type ConsoleSection, type ConsoleSectionId } from './sections';

// pathForSection maps each section 1:1 onto its own path -- overview stays the
// root path so a plain '/' (no history yet, or the fixed post-OIDC redirect
// target every sign-in lands on) resolves to the same section it always did.
export function pathForSection(id: ConsoleSectionId): string {
  return id === 'overview' ? '/' : `/${id}`;
}

function isConsoleSectionId(value: string): value is ConsoleSectionId {
  return (ALL_SECTION_IDS as string[]).includes(value);
}

function sectionIdFromPath(pathname: string): ConsoleSectionId | undefined {
  if (pathname === '/' || pathname === '') {
    return 'overview';
  }
  const candidate = pathname.slice(1);
  return isConsoleSectionId(candidate) ? candidate : undefined;
}

// resolveSection validates a candidate URL against the tenant's actually
// permitted sections (sections.ts's OPERATIONS gating), so a deep link, a
// restored reload, or a Back/Forward step can never land on a panel the
// backend would 403 -- the same reason the nav itself never renders an entry
// the tenant can't reach. Falls back to Overview for anything unrecognised or
// not currently permitted.
export function resolveSection(pathname: string, permitted: ConsoleSection[]): ConsoleSectionId {
  const fromUrl = sectionIdFromPath(pathname);
  return fromUrl !== undefined && permitted.some((section) => section.id === fromUrl)
    ? fromUrl
    : 'overview';
}
