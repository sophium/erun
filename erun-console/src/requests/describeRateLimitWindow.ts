// describeRateLimitWindow turns the raw admission-window seconds into the
// consequence sentence: say what the number means, not just the number.
// The limiter (InviteRequestRateLimiter.allowIdentity,
// erun-backend-api/internal/routes/invite_requests.go) is a per-identity
// fixed-window bucket with limit 1, keyed on the verified (issuer, subject).
// An identity with no bucket yet -- i.e. any first-time applicant -- is
// always admitted regardless of the window's length; the window only bounds
// how soon that *same* identity may submit again (SubmitInviteRequest updates
// a still-pending request in place, so a repeat submission is mostly an
// edit). A long window never closes new applications, and the sentence must
// not imply it does.
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
