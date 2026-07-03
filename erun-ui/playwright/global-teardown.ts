import { deleteK3dCluster } from './fixtures/k3dCluster.js';
import { e2eK3dEnabled, removeIsolatedRoot } from './fixtures/seedRoot.js';

// Reset to a clean slate between back-to-back runs; run.sh's EXIT trap is the backstop for an aborted run.
export default function globalTeardown(): void {
  if (e2eK3dEnabled()) {
    deleteK3dCluster();
  }
  removeIsolatedRoot();
}
