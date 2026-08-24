import { BrowserOpenURL } from '../../wailsjs/runtime/runtime';
import type { AppThunk } from './store';

// The public product documentation site (erun-docs/docusaurus.config.ts).
// First run has no in-app route to it otherwise: prose can name a settings
// path, but only this gives a reader somewhere to click.
const ERUN_DOCS_URL = 'https://docs.erunpaas.com';

// openDocumentation goes through the Wails runtime binding, not
// window.open: a plain window.open from the React side stays inside the
// desktop's WKWebView and never reaches an external browser (see
// startContributeApp in contribute_mode.go for the same reasoning). The
// headless Playwright harness shims this same binding to a real
// window.open, so it is also the one call that is safe there.
export const openDocumentation = (): AppThunk => () => {
  BrowserOpenURL(ERUN_DOCS_URL);
};
