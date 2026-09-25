import type { Page } from '@playwright/test';

// injectTestStylesheet appends one test-only stylesheet to the live document,
// keyed by its own attribute so a repeated call is a no-op and a re-navigation
// ("reboot()") gets the rule back.
//
// Injected with `page.evaluate`, not `page.addStyleTag`. addStyleTag appends
// the element and then awaits the element's `load` event, which HTML does not
// define for an inline <style>; it also issues that call with no timeout of
// its own, so a round trip that stalls does not give up -- it silently
// consumes whatever budget the calling test had left, and the spec reports a
// bare test timeout naming addStyleTag rather than the measurement it was
// about to make. One evaluate is a single bounded round trip with nothing to
// wait on but the document itself.
//
// It lives beside the page objects rather than under fixtures/: this module
// imports nothing but @playwright/test, so a POM (pages/Sidebar.ts) and
// fixtures/erunApp.ts can both reach it -- erunApp already imports pages/ for
// AppShell, so the dependency points the way it already did instead of adding
// a pages -> fixtures edge. Shared non-POM helpers are not a new kind of
// resident here; TerminalPane exports captureInvokes and parseInvoke the same
// way.
export async function injectTestStylesheet(page: Page, attr: string, css: string): Promise<void> {
  await page.evaluate(
    ({ css, attr }) => {
      if (document.head.querySelector(`style[${attr}]`)) {
        return;
      }
      const style = document.createElement('style');
      style.setAttribute(attr, '');
      style.textContent = css;
      document.head.append(style);
    },
    { css, attr },
  );
}
