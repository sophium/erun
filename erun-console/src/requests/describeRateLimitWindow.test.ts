import { describe, expect, it } from 'vitest';

import { describeRateLimitWindow } from './describeRateLimitWindow';

describe('describeRateLimitWindow', () => {
  it('describes a sub-5-minute window in seconds', () => {
    expect(describeRateLimitWindow(60)).toBe('Applicants can submit roughly every 60 seconds.');
    expect(describeRateLimitWindow(1)).toBe('Applicants can submit roughly every 1 second.');
  });

  it('describes a 5-minute-to-1-hour window in minutes', () => {
    expect(describeRateLimitWindow(600)).toBe('Applicants can submit roughly every 10 minutes.');
  });

  it('describes a 1-hour-to-1-day window in hours', () => {
    expect(describeRateLimitWindow(7200)).toBe('Applicants can submit roughly every 2 hours.');
  });

  it('describes a day-or-longer window as an effective signup freeze', () => {
    expect(describeRateLimitWindow(86400)).toBe(
      'New applications are effectively closed for about 1 day.',
    );
    expect(describeRateLimitWindow(172800)).toBe(
      'New applications are effectively closed for about 2 days.',
    );
  });
});
