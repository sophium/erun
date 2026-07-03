import { createK3dCluster } from './fixtures/k3dCluster.js';
import {
  createIsolatedLayout,
  e2eK3dEnabled,
  seedBaseline,
  seedBaselineForK3d,
} from './fixtures/seedRoot.js';

// globalSetup runs in the Playwright runner process before the webServer
// (erun-app --headless) boots, so the isolated root and the deterministic
// baseline exist by the time the backend reads its config tree. See
// fixtures/seedRoot.ts for the layout and the seeded names.
export default function globalSetup(): void {
  createIsolatedLayout();
  if (e2eK3dEnabled()) {
    // Opt-in k3d e2e mode: a minimal baseline (no inert
    // `test-context` envs) plus a real local cluster the e2e specs deploy to.
    seedBaselineForK3d();
    createK3dCluster();
    return;
  }
  seedBaseline();
}
