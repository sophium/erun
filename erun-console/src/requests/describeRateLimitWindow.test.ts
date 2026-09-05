import { describe, expect, it } from 'vitest';

import { describeRateLimitWindow } from './describeRateLimitWindow';

describe('describeRateLimitWindow', () => {
  it('describes a sub-5-minute window in seconds, scoped to the same applicant resubmitting', () => {
    expect(describeRateLimitWindow(60)).toBe(
      'The same applicant can resubmit (edit a pending request) roughly every 60 seconds. New applicants are never limited by this.',
    );
    expect(describeRateLimitWindow(1)).toBe(
      'The same applicant can resubmit (edit a pending request) roughly every 1 second. New applicants are never limited by this.',
    );
  });

  it('describes a 5-minute-to-1-hour window in minutes', () => {
    expect(describeRateLimitWindow(600)).toBe(
      'The same applicant can resubmit (edit a pending request) roughly every 10 minutes. New applicants are never limited by this.',
    );
  });

  it('describes a 1-hour-to-1-day window in hours', () => {
    expect(describeRateLimitWindow(7200)).toBe(
      'The same applicant can resubmit (edit a pending request) roughly every 2 hours. New applicants are never limited by this.',
    );
  });

  it('never claims a day-or-longer window closes new applications -- only the same applicant is limited', () => {
    expect(describeRateLimitWindow(86400)).toBe(
      'The same applicant can resubmit (edit a pending request) only about once every 1 day. New applications are still accepted immediately -- this never closes signups.',
    );
    expect(describeRateLimitWindow(172800)).toBe(
      'The same applicant can resubmit (edit a pending request) only about once every 2 days. New applications are still accepted immediately -- this never closes signups.',
    );
  });
});
