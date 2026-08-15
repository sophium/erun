import type { UIVersionSuggestion } from '@/types';

import { normalizeDialogValue, versionChoiceSource } from './versionSuggestions';

// The runtime chart and the runtime image are two artifacts on two release
// lines. A version names both only while they ride one line, which is why an
// environment whose image is versioned on its project's own line states the
// chart separately (EnvConfig.runtimechart).
//
// The choices offered here are ERun-line versions only. ERun publishes
// charts/erun-devops and the erun-devops image together at one version, so every
// listed ERun version is a chart that exists -- an offered choice can be trusted.
// A tenant's own image version implies nothing about a tenant chart at that
// version (a project may publish no charts at all), so those are deliberately
// not offered: they would invite the very deploy failure this control exists to
// prevent. A tenant umbrella needs no entry anyway -- the paired default already
// resolves it when it is published.

export const ERUN_CHART_NAME = 'erun-devops';

export interface RuntimeChartChoice {
  // label is what the operator reads: "ERun 1.0.178".
  label: string;
  // reference is what the environment records: a full OCI chart reference.
  reference: string;
  version: string;
}

// runtimeChartRegistry reads the registry namespace out of an image reference --
// `ghcr.io/sophium/erun-devops` names the registry `ghcr.io/sophium`, which is
// where that line's charts are published too.
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
