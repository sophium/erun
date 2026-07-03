import { createK3dCluster } from './fixtures/k3dCluster.js';
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
    createK3dCluster();
    return;
  }
  seedBaseline();
}
