import type { UIErunVersion, UIRuntimeVersionLine } from '@/uiRuntimeVersionLineTypes';

// RuntimeVersionLineSummary is the hover card's Version row: the number plus a
// caption naming its release line, since a bare number reads as an erun version.
export interface RuntimeVersionLineSummary {
  hasVersion: boolean;
  version: string;
  // caption is '' for an environment that has never deployed.
  caption: string;
}

export function summarizeRuntimeVersionLine(
  runtimeVersion: string,
  line: UIRuntimeVersionLine | undefined,
): RuntimeVersionLineSummary {
  const version = runtimeVersion.trim();
  if (!version) {
    return { hasVersion: false, version: '', caption: '' };
  }
  return { hasVersion: true, version, caption: runtimeVersionLineCaption(line) };
}

function runtimeVersionLineCaption(line: UIRuntimeVersionLine | undefined): string {
  if (!line) {
    return '';
  }
  if (line.undetermined) {
    return 'Line undetermined — redeploy to record it';
  }
  return runtimeVersionLineSegments(line).join(' — ');
}

function runtimeVersionLineSegments(line: UIRuntimeVersionLine): string[] {
  const lineName = line.line?.trim();
  const image = line.image?.trim();
  const head = [lineName ? `${lineName} line` : '', image].filter(Boolean).join(' · ');
  const segments = head ? [head] : [];
  if (line.disagrees) {
    segments.push('release name disagrees with the image');
  }
  return segments;
}

// ErunVersionSummary is the hover card's erun version row; null when never deployed.
export interface ErunVersionSummary {
  // known is false when config alone cannot tell; the row renders "Undetermined".
  known: boolean;
  sameAsRuntime: boolean;
  version: string;
}

export function summarizeErunVersion(
  hasRuntimeVersion: boolean,
  erunVersion: UIErunVersion | undefined,
): ErunVersionSummary | null {
  if (!hasRuntimeVersion) {
    return null;
  }
  const version = erunVersion?.version?.trim() ?? '';
  if (!version) {
    return { known: false, sameAsRuntime: false, version: '' };
  }
  return { known: true, sameAsRuntime: erunVersion?.sameAsRuntimeVersion === true, version };
}
