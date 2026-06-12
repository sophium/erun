import { removeIsolatedRoot } from './fixtures/seedRoot.js';

// globalTeardown removes the suite-owned isolated root after the run so
// back-to-back runs start from the same clean slate. run.sh also removes
// the root it created via its EXIT trap, covering aborted runs.
export default function globalTeardown(): void {
  removeIsolatedRoot();
}
