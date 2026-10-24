import { deleteK3dCluster } from './fixtures/k3dCluster.js';
import { e2eK3dEnabled, removeIsolatedRoot } from './fixtures/seedRoot.js';

export default function globalTeardown(): void {
  if (e2eK3dEnabled()) {
    deleteK3dCluster();
  }
  removeIsolatedRoot();
}
