import {
  createK3dCluster,
  e2eRealClusterContext,
  useExistingCluster,
} from './fixtures/k3dCluster.js';
import {
  createIsolatedLayout,
  e2eK3dEnabled,
  seedBaseline,
  seedBaselineForK3d,
} from './fixtures/seedRoot.js';

// Runs before the backend boots so the isolated config root and seeded
// baseline exist by the time it reads its config tree.
export default function globalSetup(): void {
  createIsolatedLayout();
  if (e2eK3dEnabled()) {
    seedBaselineForK3d();
    const realCtx = e2eRealClusterContext();
    if (realCtx !== '') {
      // Drive the developer's already-running cluster (erun-k3s) rather than a
      // fresh k3d one — the create→deploy loop only reproduces there.
      useExistingCluster(realCtx);
    } else {
      createK3dCluster();
    }
    return;
  }
  seedBaseline();
}
