import { WebLinksAddon } from '@xterm/addon-web-links';
import type { IDisposable, Terminal } from '@xterm/xterm';
import { noop } from 'erun-kit';

import { BrowserOpenURL } from '../../wailsjs/runtime/runtime';
import { store } from './store';
import { createPathLinkProvider } from './terminalPathLinkProvider';
import { activatableUrl, TERMINAL_URL_REGEX } from './terminalUrlLinks';

// xterm's Linkifier remembers the last hovered buffer cell
// (`_lastBufferCell`) and treats a mousemove into that same cell as a no-op,
// so it never re-asks its link providers -- not even after a real
// mouseleave-then-mouseenter back onto the exact same screen position. A
// pointer that stays put while new output overwrites what's under it is
// already handled correctly (xterm re-asks for the currently
// hovered link when its own row repaints), but a pointer that leaves the
// terminal and returns to that same pixel -- an entirely ordinary
// interaction, re-checking a link before clicking it, or glancing away and
// back -- gets no hover re-evaluation at all: the cursor-pointer decoration
// silently never reappears, whether or not the content underneath changed
// while it was away. Force a real re-evaluation on every departure by
// bouncing xterm's own tracked position off a neutral cell first, so the
// next real mousemove is never a same-cell no-op no matter where the pointer
// lands when it returns. The neutral cell is picked from whichever vertical
// half the pointer was NOT just in -- row 0 (where a freshly cleared screen's
// first line, and so its first link, almost always sits) is the single most
// common real hover target, so a fixed neutral position would defeat itself
// by colliding with exactly the case this exists to fix. `resetting` guards
// against the synthetic mouseleave this dispatches re-entering this handler.
function installLinkHoverInvalidation(terminal: Terminal): IDisposable {
  const screen = terminal.element?.querySelector<HTMLElement>('.xterm-screen');
  if (!screen) {
    return { dispose: noop };
  }
  let lastClientY = 0;
  const trackPosition = (event: MouseEvent): void => {
    lastClientY = event.clientY;
  };
  let resetting = false;
  const resetHoverTracking = (): void => {
    if (resetting) {
      return;
    }
    resetting = true;
    try {
      const rect = screen.getBoundingClientRect();
      const clientX = rect.left + 1;
      const clientY = lastClientY < rect.top + rect.height / 2 ? rect.bottom - 1 : rect.top + 1;
      screen.dispatchEvent(new MouseEvent('mousemove', { clientX, clientY }));
      screen.dispatchEvent(new MouseEvent('mouseleave', { clientX, clientY }));
    } finally {
      resetting = false;
    }
  };
  screen.addEventListener('mousemove', trackPosition);
  screen.addEventListener('mouseleave', resetHoverTracking);
  return {
    dispose: () => {
      screen.removeEventListener('mousemove', trackPosition);
      screen.removeEventListener('mouseleave', resetHoverTracking);
    },
  };
}

// installTerminalLinkHandling wires the three link mechanisms terminal output
// can carry, in the order xterm resolves them: an OSC 8 hyperlink (a program
// declaring what its output means) takes priority over pattern matching, then
// http(s)/mailto URL matching, then absolute file paths. All three funnel
// through activatableUrl/OpenHostPath rather than window.open, matching the
// BrowserOpenURL discipline documentationThunks already established, and all
// three restrict activation to http/https/mailto -- a terminal renders
// untrusted output, so javascript:/data:/file: must never be activatable.
// Also installs the stale-hover-cache workaround above. Returns the one
// disposable the caller must clean up on unmount (the web links addon and the
// OSC 8 handler are owned by the terminal instance itself).
export function installTerminalLinkHandling(terminal: Terminal): IDisposable {
  const webLinksAddon = new WebLinksAddon(
    (_event, uri) => {
      if (activatableUrl(uri)) {
        BrowserOpenURL(uri);
      }
    },
    { urlRegex: TERMINAL_URL_REGEX },
  );
  terminal.loadAddon(webLinksAddon);
  terminal.options.linkHandler = {
    activate: (_event, text) => {
      if (activatableUrl(text)) {
        BrowserOpenURL(text);
      }
    },
    // The scheme allowlist above is the real restriction; this only lets a
    // non-http OSC 8 URI (mailto) reach that check instead of being dropped
    // by xterm before activatableUrl ever sees it.
    allowNonHttpProtocols: true,
  };
  const pathLinkProviderDisposable = terminal.registerLinkProvider(
    createPathLinkProvider(terminal, {
      activeSessionId: () => store.getState().terminal.sessionId,
    }),
  );
  const hoverInvalidationDisposable = installLinkHoverInvalidation(terminal);
  return {
    dispose: () => {
      pathLinkProviderDisposable.dispose();
      hoverInvalidationDisposable.dispose();
    },
  };
}
