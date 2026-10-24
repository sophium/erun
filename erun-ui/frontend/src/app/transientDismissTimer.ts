// scheduleTransientDismiss counts only focused time, so a toast shown while the
// operator is in another app is not dismissed unseen.
export function scheduleTransientDismiss(durationMs: number, onDismiss: () => void): () => void {
  let remainingMs = durationMs;
  let startedAt = 0;
  let timer = 0;

  const cleanup = (): void => {
    window.clearTimeout(timer);
    window.removeEventListener('blur', onBlur);
    window.removeEventListener('focus', onFocus);
  };

  function armIfFocused(): void {
    if (!document.hasFocus()) {
      return;
    }
    startedAt = Date.now();
    timer = window.setTimeout(() => {
      cleanup();
      onDismiss();
    }, remainingMs);
  }

  function onBlur(): void {
    window.clearTimeout(timer);
    remainingMs = Math.max(0, remainingMs - (Date.now() - startedAt));
  }

  function onFocus(): void {
    armIfFocused();
  }

  window.addEventListener('blur', onBlur);
  window.addEventListener('focus', onFocus);
  armIfFocused();

  return cleanup;
}
