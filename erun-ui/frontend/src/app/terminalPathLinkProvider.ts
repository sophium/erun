import type { IBufferLine, ILink, ILinkProvider, Terminal } from '@xterm/xterm';

import { OpenHostPath, ResolveEnvironmentHostPath } from '../../wailsjs/go/main/App';
import { readError } from './errors';
import { showNotification } from './notificationThunks';
import { store } from './store';
import type { TerminalOrigin } from './terminalOrigin';
import { terminalOriginForSession } from './terminalOrigin';
import { findAbsolutePathMatches, type PathMatch } from './terminalPathLinks';

export interface PathLinkProviderDeps {
  activeSessionId: () => number;
}

type PathResolution =
  | { readonly activatable: true; readonly open: () => Promise<void> }
  | { readonly activatable: false; readonly reason: string };

// This desktop's own opener never resolves a pod path against the host --
// this branch classifying the tab as host-side means it genuinely runs on the
// operator's machine (the Local tab, an orchestrator, or the contribute
// shell), so its own printed paths are real host paths.
function hostResolution(text: string): PathResolution {
  return {
    activatable: true,
    open: () => OpenHostPath(text),
  };
}

async function podResolution(
  tenant: string,
  environment: string,
  text: string,
): Promise<PathResolution> {
  const result = await ResolveEnvironmentHostPath({ tenant, environment }, text);
  if (result.kind === 'unresolved' || !result.hostPath) {
    return { activatable: false, reason: result.reason ?? 'this path has no host equivalent' };
  }
  const hostPath = result.hostPath;
  return { activatable: true, open: () => OpenHostPath(hostPath) };
}

function resolveMatch(origin: TerminalOrigin, match: PathMatch): Promise<PathResolution> {
  if (origin.kind === 'host') {
    return Promise.resolve(hostResolution(match.text));
  }
  if (origin.kind === 'pod') {
    return podResolution(origin.tenant, origin.environment, match.text);
  }
  return Promise.resolve({
    activatable: false,
    reason: "this tab's location could not be determined, so its paths cannot be opened",
  });
}

// bufferColumnForIndex maps a 0-based string offset from IBufferLine's
// translateToString(true) back onto a buffer column, mirroring the approach
// @xterm/addon-web-links's LinkComputer uses internally (that helper is not
// part of the addon's public API, so this is a single-line-only version --
// a path that wraps across terminal rows is not matched, a documented
// limitation).
function bufferColumnForIndex(terminal: Terminal, line: IBufferLine, index: number): number | null {
  const cell = terminal.buffer.active.getNullCell();
  let consumed = 0;
  for (let x = 0; x < line.length; x++) {
    if (consumed === index) {
      return x;
    }
    line.getCell(x, cell);
    const width = cell.getWidth();
    if (width) {
      consumed += cell.getChars().length || 1;
    }
  }
  return consumed === index ? line.length : null;
}

function reportOpenFailure(error: unknown): void {
  store.dispatch(showNotification('error', readError(error)));
}

function buildLink(
  y: number,
  startX: number,
  endX: number,
  match: PathMatch,
  resolution: PathResolution,
): ILink {
  return {
    range: { start: { x: startX + 1, y }, end: { x: endX, y } },
    text: match.text,
    decorations: { pointerCursor: resolution.activatable, underline: resolution.activatable },
    activate: () => {
      if (resolution.activatable) {
        resolution.open().catch(reportOpenFailure);
      } else {
        store.dispatch(showNotification('info', resolution.reason));
      }
    },
  };
}

// createPathLinkProvider registers absolute file paths as terminal links,
// resolved against the tab's own filesystem: a host-side tab opens the path
// directly, a pod-side tab is mapped onto its environment's host mirror (or
// left unresolved with a stated reason) -- never resolved against a
// same-named host file (#1354).
export function createPathLinkProvider(
  terminal: Terminal,
  deps: PathLinkProviderDeps,
): ILinkProvider {
  return {
    provideLinks(y: number, callback: (links: ILink[] | undefined) => void): void {
      const line = terminal.buffer.active.getLine(y - 1);
      if (!line) {
        callback(undefined);
        return;
      }
      const text = line.translateToString(true);
      const matches = findAbsolutePathMatches(text);
      if (matches.length === 0) {
        callback(undefined);
        return;
      }
      const origin = terminalOriginForSession(store.getState(), deps.activeSessionId());
      void Promise.all(matches.map((match) => resolveMatch(origin, match))).then((resolutions) => {
        const links: ILink[] = [];
        for (let i = 0; i < matches.length; i++) {
          const match = matches[i];
          const resolution = resolutions[i];
          if (!match || !resolution) {
            continue;
          }
          const startX = bufferColumnForIndex(terminal, line, match.start);
          const endX = bufferColumnForIndex(terminal, line, match.end);
          if (startX === null || endX === null) {
            continue;
          }
          links.push(buildLink(y, startX, endX, match, resolution));
        }
        callback(links.length > 0 ? links : undefined);
      });
    },
  };
}
