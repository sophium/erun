import type { Page } from '@playwright/test';

// Uses page.evaluate, not addStyleTag: addStyleTag awaits a `load` event inline styles
// never fire, with no timeout, so a stall consumes the test's whole budget.
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
