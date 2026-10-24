import type { UISelection, UIVersionSuggestion, UIVersionSuggestionNotice } from '@/types';

export function findVersionSuggestion(
  suggestions: UIVersionSuggestion[],
  version: string,
  image: string,
): UIVersionSuggestion | undefined {
  if (!version) {
    return undefined;
  }
  if (image) {
    return suggestions.find(
      (suggestion) => suggestion.version === version && suggestion.image === image,
    );
  }
  return suggestions.find((suggestion) => suggestion.version === version);
}

export function normalizeDialogValue(value: string): string {
  return value.trim();
}

export function normalizeVersionSuggestions(values: UIVersionSuggestion[]): UIVersionSuggestion[] {
  const suggestions: UIVersionSuggestion[] = [];
  for (const value of values) {
    const version = normalizeDialogValue(value.version);
    const image = normalizeDialogValue(value.image ?? '');
    const source = normalizeDialogValue(value.source ?? '');
    const label = normalizeDialogValue(value.label);
    if (
      version &&
      !suggestions.some(
        (suggestion) =>
          suggestion.version === version &&
          suggestion.image === image &&
          suggestion.source === source &&
          suggestion.label === label,
      )
    ) {
      suggestions.push({
        label,
        version,
        source,
        image,
      });
    }
  }
  return suggestions;
}

export function versionNoticeMessage(notice: UIVersionSuggestionNotice): string {
  if (notice.kind === 'auth') {
    return `${notice.image} is private — ${registrySignInHint(notice.image)} to list its versions.`;
  }
  return `${notice.image} — couldn't reach the registry to list its versions.`;
}

// registrySignInHint names the image's own registry, not always ghcr.
function registrySignInHint(image: string): string {
  const host = registryHostFromImage(image);
  if (host === 'ghcr.io') {
    return 'run docker login ghcr.io (or gh auth login)';
  }
  return `run docker login ${host || 'docker.io'}`;
}

// A first segment with a dot or port is a registry host, else a Docker Hub
// namespace, matching the backend.
function registryHostFromImage(image: string): string {
  const first = normalizeDialogValue(image).split('/')[0] ?? '';
  return first.includes('.') || first.includes(':') ? first : '';
}

export function normalizeVersionSuggestionNotices(
  values: UIVersionSuggestionNotice[],
): UIVersionSuggestionNotice[] {
  const notices: UIVersionSuggestionNotice[] = [];
  for (const value of values) {
    const image = normalizeDialogValue(value.image);
    const kind = normalizeDialogValue(value.kind);
    if (image && !notices.some((notice) => notice.image === image && notice.kind === kind)) {
      notices.push({ image, kind });
    }
  }
  return notices;
}

export function versionChoiceLabel(suggestion: UIVersionSuggestion): string {
  const source = versionChoiceSource(suggestion);
  if (!suggestion.label) {
    if (source) {
      return `${source}: ${suggestion.version}`;
    }
    return suggestion.version;
  }
  if (source && !suggestion.label.toLowerCase().startsWith(source.toLowerCase())) {
    return `${source} ${suggestion.label.toLowerCase()}: ${suggestion.version}`;
  }
  return `${suggestion.label}: ${suggestion.version}`;
}

export function versionChoiceKind(suggestion: UIVersionSuggestion): string {
  const label = normalizeDialogValue(suggestion.label);
  if (!label) {
    return '';
  }
  const source = versionChoiceSource(suggestion);
  if (source && label.toLowerCase().startsWith(source.toLowerCase())) {
    return normalizeDialogValue(label.slice(source.length));
  }
  return label;
}

export function versionChoiceSource(suggestion: UIVersionSuggestion): string {
  const source = normalizeDialogValue(suggestion.source ?? '');
  if (source) {
    return source;
  }
  const image = normalizeDialogValue(suggestion.image ?? '');
  if (image === 'erun-devops') {
    return 'ERun';
  }
  if (image.endsWith('-devops')) {
    return image.slice(0, -'-devops'.length);
  }
  return '';
}

export function versionChoiceImage(suggestion: UIVersionSuggestion): string {
  const image = normalizeDialogValue(suggestion.image ?? '');
  if (image) {
    return image;
  }
  const source = versionChoiceSource(suggestion);
  if (!source) {
    return '';
  }
  if (source === 'ERun') {
    return 'erun-devops';
  }
  return `${source}-devops`;
}

export interface VersionSuggestionGroup {
  source: string;
  heading: string;
  suggestions: UIVersionSuggestion[];
}

// groupVersionSuggestionsBySource keeps first-seen order, so a tenant's own line
// lists before the upstream ERun line.
export function groupVersionSuggestionsBySource(
  suggestions: UIVersionSuggestion[],
): VersionSuggestionGroup[] {
  const groups: VersionSuggestionGroup[] = [];
  for (const suggestion of suggestions) {
    const source = versionChoiceSource(suggestion);
    const existing = groups.find((group) => group.source === source);
    if (existing) {
      existing.suggestions.push(suggestion);
      continue;
    }
    groups.push({ source, heading: versionSourceHeading(source), suggestions: [suggestion] });
  }
  return groups;
}

export function versionSourceHeading(source: string): string {
  if (!source || source === 'ERun') {
    return 'Upstream ERun';
  }
  return `This environment (${source})`;
}

export function selectedVersionSourceText(suggestion: UIVersionSuggestion | undefined): string {
  if (!suggestion) {
    return '';
  }
  const image = versionChoiceImage(suggestion);
  if (!image) {
    return '';
  }
  return `Image: ${image}`;
}

export function deleteConfirmationValue(selection: UISelection): string {
  return `${selection.tenant}-${selection.environment}`;
}

export function selectionKey(selection: UISelection): string {
  return `${selection.tenant}\u0000${selection.environment}`;
}
