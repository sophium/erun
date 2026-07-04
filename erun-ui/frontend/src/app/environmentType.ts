// `type` is the single source of truth for an env's shape; these classifiers
// must stay in sync with the deciders in erun-common/config.go.

// environmentTypeIsRemoteWorktree reports whether the env's worktree lives off
// this machine. An empty/unset type is treated as local so an unresolved env
// keeps local behaviour.
export function environmentTypeIsRemoteWorktree(type: string | undefined): boolean {
  return type === 'remote-agent' || type === 'runtime';
}

// environmentTypeIsRuntime reports whether the env is a runtime (serving) env —
// the only type that can opt into a mounted source worktree. Mirrors
// EnvironmentTypeRuntime in erun-common/config.go.
export function environmentTypeIsRuntime(type: string | undefined): boolean {
  return type === 'runtime';
}

// environmentTypeBuildsHere reports whether the env builds its own runtime
// image rather than consuming a published one. An empty/unset type is treated
// as building so an unresolved env keeps today's behaviour.
export function environmentTypeBuildsHere(type: string | undefined): boolean {
  return type !== 'runtime';
}
