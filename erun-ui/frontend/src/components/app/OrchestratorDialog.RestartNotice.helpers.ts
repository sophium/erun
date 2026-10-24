import type { OrchestratorInfo } from '@/app/slices/orchestratorsSlice';

// restartNoticeText: only link changes claim missing tools; role and launch-setting changes get their own wording.
export function restartNoticeText(editing: OrchestratorInfo): string | null {
  if (editing.restartRequired) {
    return 'Its environments changed while it was running. The session still holds tools for what it was linked to before and none for what was added since.';
  }
  if (editing.roleChanged) {
    return "A linked environment's role changed while it was running. It's saved, but the session's current context may still reflect the old one — restart to be sure it picks up the change.";
  }
  if (editing.launchSettingsChanged) {
    return 'Its gateway, model or effort changed while it was running. The change is saved; the session keeps what it launched with until it restarts.';
  }
  return null;
}
