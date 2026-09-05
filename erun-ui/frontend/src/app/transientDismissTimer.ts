// scheduleTransientDismiss runs onDismiss after durationMs of the window
// actually being focused -- "rendered" is not "read": a toast that pops while
// the operator is in another application must not silently mark itself seen
// just because a JS timer happened to elapse while nobody was looking. The
// countdown pauses on window blur and resumes (with whatever time was left)
// on focus, the same document.hasFocus() signal terminalFocus.ts already
// uses for "is anyone actually looking at this window right now". Returns a
// cancel function for a caller that needs to abandon the countdown early
// (a fresh report replacing an unfired one, hover holding it open).
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
