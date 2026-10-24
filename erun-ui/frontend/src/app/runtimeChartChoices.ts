import type { UIVersionSuggestion } from '@/types';

import { normalizeDialogValue, versionChoiceSource } from './versionSuggestions';

// Only ERun-line versions are offered: ERun publishes the chart and image
// together, while a tenant image version implies no tenant chart exists.

export const ERUN_CHART_NAME = 'erun-devops';

export interface RuntimeChartChoice {
  label: string;
  // reference is a full OCI chart reference.
  reference: string;
  version: string;
}

export function runtimeChartRegistry(image: string): string {
  const reference = normalizeDialogValue(image);
  const cut = reference.lastIndexOf('/');
  if (cut <= 0) {
    return '';
  }
  return reference.slice(0, cut);
}

export function runtimeChartReference(registry: string, version: string): string {
  const namespace = normalizeDialogValue(registry);
  const tag = normalizeDialogValue(version);
  if (!namespace || !tag) {
    return '';
  }
  return `oci://${namespace}/charts/${ERUN_CHART_NAME}:${tag}`;
}

export function runtimeChartChoices(suggestions: UIVersionSuggestion[]): RuntimeChartChoice[] {
  const choices: RuntimeChartChoice[] = [];
  for (const suggestion of suggestions) {
    if (versionChoiceSource(suggestion) !== 'ERun') {
      continue;
    }
    const registry = runtimeChartRegistry(suggestion.image ?? '');
    const reference = runtimeChartReference(registry, suggestion.version);
    if (!reference || choices.some((choice) => choice.reference === reference)) {
      continue;
    }
    choices.push({
      label: suggestion.label || `ERun ${suggestion.version}`,
      reference,
      version: normalizeDialogValue(suggestion.version),
    });
  }
  return choices;
}
