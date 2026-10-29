import {
  createK3dCluster,
  e2eRealClusterContext,
  useExistingCluster,
} from './fixtures/k3dCluster.js';
import { createIsolatedLayout, e2eK3dEnabled, seedBaselineForK3d } from './fixtures/seedRoot.js';

// Only e2e-k3d mode needs global setup: one shared root, backend and cluster.
export default function globalSetup(): void {
  if (!e2eK3dEnabled()) {
    return;
  }
  createIsolatedLayout();
  seedBaselineForK3d();
  const realCtx = e2eRealClusterContext();
  if (realCtx !== '') {
    // The create→deploy loop only reproduces on the developer's real cluster.
    useExistingCluster(realCtx);
  } else {
    createK3dCluster();
  }
}
