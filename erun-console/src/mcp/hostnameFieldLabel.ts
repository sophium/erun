// hostnameFieldLabel says whether a hostname field is prefilled from a known,
// discovered edge (still editable, to override) or falls back to plain
// manual entry because the environment has never been successfully exposed.
// Shared between DriveToolForm/AttachSessionForm (MCPAccessPanel.tsx) and
// OperateToolForm.
export function hostnameFieldLabel(prefix: string, exposedHostname: string | undefined): string {
  return exposedHostname !== undefined
    ? `${prefix} (discovered — edit to override)`
    : `${prefix} (not yet exposed — enter one)`;
}
