import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { PIPELINE_LADDER, RUNG_LABELS, rungLabel, rungTone } from './pipelineRungs';

describe('pipeline rungs', () => {
  it('names every rung of the ladder', () => {
    for (const rung of PIPELINE_LADDER) {
      assert.ok(RUNG_LABELS[rung], `${rung} has no label`);
      assert.notEqual(rungLabel(rung), rung, `${rung} is rendered under its own spelling`);
    }
  });

  it('renders an unrecognised rung under its own spelling', () => {
    assert.equal(rungLabel('QUARANTINED'), 'QUARANTINED');
  });

  it('tones a rung it does not know as neither progress nor failure', () => {
    assert.equal(rungTone('QUARANTINED'), 'muted');
  });

  it('never tones an outcome as a step still moving', () => {
    assert.notEqual(rungTone('ABANDONED'), rungTone('IN_PROGRESS'));
    assert.notEqual(rungTone('FAILED'), rungTone('IN_PROGRESS'));
  });
});
