// hostnameFieldLabel distinguishes a discovered (still editable) hostname from manual entry.
export function hostnameFieldLabel(prefix: string, exposedHostname: string | undefined): string {
  return exposedHostname !== undefined
    ? `${prefix} (discovered — edit to override)`
    : `${prefix} (not yet exposed — enter one)`;
}
