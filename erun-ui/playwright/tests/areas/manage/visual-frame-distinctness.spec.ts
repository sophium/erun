import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import * as path from 'node:path';

import { test, expect } from '../../../fixtures/erunApp.js';
import { expectFramesAllDistinct } from '../../../fixtures/visualFrames.js';

// The distinctness guard the visual bundles rely on is only worth having if it
// can actually fail. A guard that always passes would be the same
// silent-success defect it exists to catch -- a bundle that reads as complete
// while documenting a state the run never captured -- so both directions are
// driven against real files here rather than trusted by inspection.
//
// It lives in the manage area because that is where the guard's first call
// sites are (manage-ports-exposures.spec.ts's Ports frames).
test.describe('visual frame distinctness guard', () => {
  let dir: string;

  test.beforeEach(async () => {
    dir = await mkdtemp(path.join(tmpdir(), 'erun-frame-guard-'));
  });

  test.afterEach(async () => {
    await rm(dir, { recursive: true, force: true });
  });

  test('rejects a set containing two frames with identical bytes', async () => {
    const inFlight = path.join(dir, 'ports-create-inflight.png');
    const settled = path.join(dir, 'ports-populated.png');
    const other = path.join(dir, 'ports-empty.png');
    await writeFile(inFlight, Buffer.from('settled render'));
    await writeFile(settled, Buffer.from('settled render'));
    await writeFile(other, Buffer.from('empty render'));

    await expect(expectFramesAllDistinct([inFlight, settled, other])).rejects.toThrow(
      /are byte-identical frames for states that differ/,
    );
  });

  test('accepts a set of frames that all differ', async () => {
    const first = path.join(dir, 'ports-empty.png');
    const second = path.join(dir, 'ports-create-inflight.png');
    const third = path.join(dir, 'ports-populated.png');
    await writeFile(first, Buffer.from('empty render'));
    await writeFile(second, Buffer.from('in-flight render'));
    await writeFile(third, Buffer.from('settled render'));

    const digests = await expectFramesAllDistinct([first, second, third]);
    // Also proves the guard read every frame it was handed, rather than
    // passing vacuously over an empty set.
    expect(digests).toHaveLength(3);
  });
});
