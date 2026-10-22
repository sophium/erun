import type { OrchestratorInfo } from '@/app/slices/orchestratorsSlice';

// restartNoticeText picks the accurate reason for the amber notice below.
// restartRequired (an environment link added or removed) always takes the
// tools-specific wording, since it is the one guaranteed true; a role-only
// edit (roleChanged) gets its own honest wording instead of being folded into
// the same sentence — a role never changes which MCP tools the session holds,
// so claiming "tools are missing" for it would be a false diagnosis (root
// AGENTS.md § "Smooth, Seamless, No Dead Ends": distinguish causes before
// writing copy). Changed launch settings get their own wording for the same
// reason: a session fixes its gateway, model and effort when it launches.
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
