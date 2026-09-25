import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { PIPELINE_LADDER, RUNG_LABELS, rungLabel, rungTone } from './pipelineRungs';

// These pin the halves of the vocabulary that the two surfaces relying on it
// (the console's Pipeline section, the desktop's Pipeline tab) would otherwise
// each be free to answer on their own: what a rung is called, and which rung
// is a step towards merged rather than an outcome beside it.

describe('pipeline rungs', () => {
  it('names every rung of the ladder', () => {
    for (const rung of PIPELINE_LADDER) {
      assert.ok(RUNG_LABELS[rung], `${rung} has no label`);
      assert.notEqual(rungLabel(rung), rung, `${rung} is rendered under its own spelling`);
    }
  });

  it('renders an unrecognised rung under its own spelling', () => {
    // The platform being ahead of the client is not a licence to rename work
    // the client cannot see.
    assert.equal(rungLabel('QUARANTINED'), 'QUARANTINED');
  });

  it('tones a rung it does not know as neither progress nor failure', () => {
    assert.equal(rungTone('QUARANTINED'), 'muted');
  });

  it('never tones an outcome as a step still moving', () => {
    // Reading "abandoned" as "still moving" is the mistake the view exists to
    // prevent, so the outcomes are toned apart from every ladder rung.
    assert.notEqual(rungTone('ABANDONED'), rungTone('IN_PROGRESS'));
    assert.notEqual(rungTone('FAILED'), rungTone('IN_PROGRESS'));
  });
});
