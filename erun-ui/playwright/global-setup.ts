import { createIsolatedLayout, seedBaseline } from './fixtures/seedRoot.js';

// globalSetup runs in the Playwright runner process before the webServer
// (erun-app --headless) boots, so the isolated root and the deterministic
// baseline exist by the time the backend reads its config tree. See
// fixtures/seedRoot.ts for the layout and the seeded names.
export default function globalSetup(): void {
  createIsolatedLayout();
  seedBaseline();
}
