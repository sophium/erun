import { WebLinksAddon } from '@xterm/addon-web-links';
import type { IDisposable, Terminal } from '@xterm/xterm';

import { BrowserOpenURL } from '../../wailsjs/runtime/runtime';
import { store } from './store';
import { createPathLinkProvider } from './terminalPathLinkProvider';
import { activatableUrl, TERMINAL_URL_REGEX } from './terminalUrlLinks';

// installTerminalLinkHandling wires the three link mechanisms terminal output
// can carry, in the order xterm resolves them: an OSC 8 hyperlink (a program
// declaring what its output means) takes priority over pattern matching, then
// http(s)/mailto URL matching, then absolute file paths. All three funnel
// through activatableUrl/OpenHostPath rather than window.open, matching the
// BrowserOpenURL discipline documentationThunks already established, and all
// three restrict activation to http/https/mailto -- a terminal renders
// untrusted output, so javascript:/data:/file: must never be activatable.
// Returns the one disposable the caller must clean up on unmount (the web
// links addon and the OSC 8 handler are owned by the terminal instance
// itself).
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
  return terminal.registerLinkProvider(
    createPathLinkProvider(terminal, {
      activeSessionId: () => store.getState().terminal.sessionId,
    }),
  );
}
