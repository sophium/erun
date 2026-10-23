import { deleteK3dCluster } from './fixtures/k3dCluster.js';
import { e2eK3dEnabled, removeIsolatedRoot } from './fixtures/seedRoot.js';

// Reset to a clean slate between back-to-back runs. removeIsolatedRoot reaps any
// stub still parked under the root before deleting it.
export default function globalTeardown(): void {
  if (e2eK3dEnabled()) {
    deleteK3dCluster();
  }
  removeIsolatedRoot();
}
