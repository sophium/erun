import { ALL_SECTION_IDS, type ConsoleSection, type ConsoleSectionId } from './sections';

// Overview stays '/' because the post-OIDC redirect lands there.
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

// resolveSection falls back to Overview for an unknown or unpermitted path.
export function resolveSection(pathname: string, permitted: ConsoleSection[]): ConsoleSectionId {
  const fromUrl = sectionIdFromPath(pathname);
  return fromUrl !== undefined && permitted.some((section) => section.id === fromUrl)
    ? fromUrl
    : 'overview';
}
