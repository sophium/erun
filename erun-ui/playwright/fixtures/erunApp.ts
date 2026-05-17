import { test as base, expect } from '@playwright/test';
import { AppShell } from '../pages/index.js';

// Test fixture: each test gets a fresh AppShell wired to its own Page. The
// fixture handles boot synchronization (open + wait-for-sidebar) so spec
// bodies can jump straight to behaviour.
export const test = base.extend<{ app: AppShell }>({
  app: async ({ page }, use) => {
    const app = new AppShell(page);
    await app.open();
    await use(app);
  },
});

export { expect };
