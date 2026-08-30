// describeRateLimitWindow turns the raw admission-window seconds into the
// consequence sentence issue #1682 §9 demands ("say what the number means,
// not just the number") -- an operator typing 86400 should read "signup
// freeze", not work that out from the digits.
export function describeRateLimitWindow(seconds: number): string {
  if (seconds < 300) {
    const unit = seconds === 1 ? 'second' : 'seconds';
    return `Applicants can submit roughly every ${String(seconds)} ${unit}.`;
  }
  if (seconds < 3600) {
    const minutes = Math.round(seconds / 60);
    const unit = minutes === 1 ? 'minute' : 'minutes';
    return `Applicants can submit roughly every ${String(minutes)} ${unit}.`;
  }
  if (seconds < 86400) {
    const hours = Math.round(seconds / 3600);
    const unit = hours === 1 ? 'hour' : 'hours';
    return `Applicants can submit roughly every ${String(hours)} ${unit}.`;
  }
  const days = Math.round(seconds / 86400);
  const unit = days === 1 ? 'day' : 'days';
  return `New applications are effectively closed for about ${String(days)} ${unit}.`;
}
