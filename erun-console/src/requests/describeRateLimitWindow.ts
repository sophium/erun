// describeRateLimitWindow explains the window's consequence. The limiter is per identity,
// so first-time applicants are always admitted; a long window never closes new applications.
export function describeRateLimitWindow(seconds: number): string {
  if (seconds < 300) {
    const unit = seconds === 1 ? 'second' : 'seconds';
    return `The same applicant can resubmit (edit a pending request) roughly every ${String(seconds)} ${unit}. New applicants are never limited by this.`;
  }
  if (seconds < 3600) {
    const minutes = Math.round(seconds / 60);
    const unit = minutes === 1 ? 'minute' : 'minutes';
    return `The same applicant can resubmit (edit a pending request) roughly every ${String(minutes)} ${unit}. New applicants are never limited by this.`;
  }
  if (seconds < 86400) {
    const hours = Math.round(seconds / 3600);
    const unit = hours === 1 ? 'hour' : 'hours';
    return `The same applicant can resubmit (edit a pending request) roughly every ${String(hours)} ${unit}. New applicants are never limited by this.`;
  }
  const days = Math.round(seconds / 86400);
  const unit = days === 1 ? 'day' : 'days';
  return `The same applicant can resubmit (edit a pending request) only about once every ${String(days)} ${unit}. New applications are still accepted immediately -- this never closes signups.`;
}
