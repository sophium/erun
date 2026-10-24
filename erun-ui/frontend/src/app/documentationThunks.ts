import { BrowserOpenURL } from '../../wailsjs/runtime/runtime';
import type { AppThunk } from './store';

const ERUN_DOCS_URL = 'https://docs.erunpaas.com';

// openDocumentation uses the Wails binding: window.open stays inside the
// desktop's WebView.
export const openDocumentation = (): AppThunk => () => {
  BrowserOpenURL(ERUN_DOCS_URL);
};

export const openInstallDocs = (): AppThunk => () => {
  BrowserOpenURL(`${ERUN_DOCS_URL}/getting-started/install`);
};

export const openPlatformBlockDocs = (): AppThunk => () => {
  BrowserOpenURL(`${ERUN_DOCS_URL}/reference/configuration#platform-block`);
};
