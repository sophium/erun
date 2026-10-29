import { WebLinksAddon } from '@xterm/addon-web-links';
import type { IDisposable, Terminal } from '@xterm/xterm';
import { noop } from 'erun-kit';

import { BrowserOpenURL } from '../../wailsjs/runtime/runtime';
import { store } from './store';
import { createPathLinkProvider } from './terminalPathLinkProvider';
import { activatableUrl, TERMINAL_URL_REGEX } from './terminalUrlLinks';

// xterm's Linkifier ignores a mousemove into its last hovered cell, so a pointer
// that leaves and returns to the same cell never re-shows the link cursor. On
// leave, bounce its tracked position off a neutral cell in the other half of
// the screen (row 0 is the common target). `resetting` stops the synthetic
// mouseleave from re-entering.
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

// installTerminalLinkHandling wires OSC 8 links, then URLs, then absolute paths.
// Terminal output is untrusted, so activation is restricted to http/https/mailto.
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
    // activatableUrl still enforces the allowlist; this only lets mailto reach it.
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
