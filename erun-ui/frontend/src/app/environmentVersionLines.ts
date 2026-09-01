import type { UIErunVersion, UIRuntimeVersionLine } from '@/uiRuntimeVersionLineTypes';

// RuntimeVersionLineSummary reduces runtimeVersion + runtimeVersionLine
// (eruncommon.RuntimeVersionLine, resolved from config alone -- see
// erun-common/runtime_version_line.go) to what the hover card's Version row
// renders: the number, and a caption naming which release line it belongs to
// -- never left bare, since a bare number reads as an erun version even when
// it names a tenant's own <tenant>-devops line.
export interface RuntimeVersionLineSummary {
  hasVersion: boolean;
  version: string;
  // caption is '' when there is no line to report -- an environment that has
  // never deployed has no RuntimeVersionLine at all.
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

// runtimeVersionLineSegments is split out of runtimeVersionLineCaption to
// keep that function's own branching within the complexity budget.
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

// ErunVersionSummary reduces the environment's erun-line version
// (eruncommon.ErunVersion) to what the hover card's second version row
// renders. null when the environment has never deployed -- there is nothing
// to annotate.
export interface ErunVersionSummary {
  // known is false when the erun version cannot be read from config alone --
  // the row renders "Undetermined" rather than guessing.
  known: boolean;
  // sameAsRuntime is true when the erun version is the runtime version's own
  // number, on erun's own line -- the row states that instead of repeating
  // the number.
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
