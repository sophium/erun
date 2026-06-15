// environmentType holds the pure classifiers for an env's `type`
// (local-agent | remote-agent | runtime). `type` is the single source of
// truth for an env's shape; the desktop reads it directly rather than the
// retired remote/snapshot booleans. Mirrors the deciders in
// erun-common/config.go (EnvConfig.RemoteWorktree / EnvConfig.BuildsHere).

// environmentTypeIsRemoteWorktree reports whether the env's worktree lives off
// this machine — true for remote-agent and runtime, false for local-agent.
// Mirrors EnvConfig.RemoteWorktree() (Type != local-agent). An empty/unset
// type is treated as local so an unresolved env keeps local behaviour.
export function environmentTypeIsRemoteWorktree(type: string | undefined): boolean {
  return type === 'remote-agent' || type === 'runtime';
}

// environmentTypeBuildsHere reports whether the env builds its runtime image
// from a worktree on the build host rather than consuming a published image.
// Mirrors EnvConfig.BuildsHere() (Type != runtime). An empty/unset type is
// treated as building so an unresolved env keeps today's behaviour.
export function environmentTypeBuildsHere(type: string | undefined): boolean {
  return type !== 'runtime';
}
