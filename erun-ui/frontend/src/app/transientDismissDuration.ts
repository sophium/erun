// TRANSIENT_DISMISS_MS is this app's one established duration for "this
// succeeded, stop showing it without being asked" -- the titlebar's
// success/info toasts (notificationThunks.ts) and the whip popover's
// all-success report (Titlebar.WhipAction.tsx) both auto-dismiss after
// exactly this long, so the two surfaces cannot drift apart.
export const TRANSIENT_DISMISS_MS = 3200;
