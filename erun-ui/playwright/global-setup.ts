import {
  createK3dCluster,
  e2eRealClusterContext,
  useExistingCluster,
} from './fixtures/k3dCluster.js';
import { createIsolatedLayout, e2eK3dEnabled, seedBaselineForK3d } from './fixtures/seedRoot.js';

// Runs before any worker starts. The default parallel mode needs nothing
// here: each worker mints its own root, seeds its own baseline, and boots its
// own backend (fixtures/workerBackend.ts). The opt-in e2e-k3d mode still
// shares one backend and one real cluster across the whole run (workers: 1),
// so it keeps the original single-root setup, done once before that one
// shared backend boots.
export default function globalSetup(): void {
  if (!e2eK3dEnabled()) {
    return;
  }
  createIsolatedLayout();
  seedBaselineForK3d();
  const realCtx = e2eRealClusterContext();
  if (realCtx !== '') {
    // Drive the developer's already-running cluster (erun-k3s) rather than a
    // fresh k3d one — the create→deploy loop only reproduces there.
    useExistingCluster(realCtx);
  } else {
    createK3dCluster();
  }
}
