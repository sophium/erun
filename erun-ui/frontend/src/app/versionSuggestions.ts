import type {
  UISelection,
  UIVersionSuggestion,
} from '@/types';

export function findVersionSuggestion(suggestions: UIVersionSuggestion[], version: string, image: string): UIVersionSuggestion | undefined {
  if (!version) {
    return undefined;
  }
  if (image) {
    return suggestions.find((suggestion) => suggestion.version === version && suggestion.image === image);
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
    const image = normalizeDialogValue(value.image || '');
    const source = normalizeDialogValue(value.source || '');
    const label = normalizeDialogValue(value.label);
    if (version && !suggestions.some((suggestion) => suggestion.version === version && suggestion.image === image && suggestion.source === source && suggestion.label === label)) {
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
  const source = normalizeDialogValue(suggestion.source || '');
  if (source) {
    return source;
  }
  const image = normalizeDialogValue(suggestion.image || '');
  if (image === 'erun-devops') {
    return 'ERun';
  }
  if (image.endsWith('-devops')) {
    return image.slice(0, -'-devops'.length);
  }
  return '';
}

export function versionChoiceImage(suggestion: UIVersionSuggestion): string {
  const image = normalizeDialogValue(suggestion.image || '');
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
  return `${selection.tenant}\u0000${selection.environment}\u0000${selection.debug === true ? 'debug' : 'normal'}`;
}
