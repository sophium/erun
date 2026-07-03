import { deleteK3dCluster } from './fixtures/k3dCluster.js';
import { e2eK3dEnabled, removeIsolatedRoot } from './fixtures/seedRoot.js';

// globalTeardown removes the suite-owned isolated root after the run so
// back-to-back runs start from the same clean slate. run.sh also removes
// the root it created via its EXIT trap, covering aborted runs.
export default function globalTeardown(): void {
  if (e2eK3dEnabled()) {
    // Tear the real k3d cluster + registry down; run.sh's EXIT
    // trap is the backstop for an aborted run.
    deleteK3dCluster();
  }
  removeIsolatedRoot();
}
