export function isNewSessionSelection(previousSessionId: number, previousKnownSessionId: number): boolean {
  return previousKnownSessionId === 0 || previousKnownSessionId !== previousSessionId;
}
