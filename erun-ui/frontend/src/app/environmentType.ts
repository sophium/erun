// `type` is the single source of truth for an env's shape; these classifiers
// must stay in sync with the deciders in erun-common/config.go.

// environmentTypeIsRemoteWorktree reports whether the env's worktree lives off
// this machine. An empty/unset type is treated as local so an unresolved env
// keeps local behaviour.
export function environmentTypeIsRemoteWorktree(type: string | undefined): boolean {
  return type === 'remote-agent' || type === 'runtime';
}

// environmentTypeIsHost reports whether the env is a host environment — a
// directory on the operator's own machine with no pod and no cluster at all.
// Mirrors EnvironmentTypeHost in erun-common/config.go.
export function environmentTypeIsHost(type: string | undefined): boolean {
  return type === 'host';
}

// environmentTypeIsRuntime reports whether the env is a runtime (serving) env —
// the only type that can opt into a mounted source worktree. Mirrors
// EnvironmentTypeRuntime in erun-common/config.go.
export function environmentTypeIsRuntime(type: string | undefined): boolean {
  return type === 'runtime';
}

// environmentTypeBuildsHereLocally reports whether the env builds its runtime
// image from source on THIS machine — a local-agent env. Only such an env can
// "create & deploy a new version" from the desktop (a remote-agent env builds in
// its own pod; a runtime env consumes published versions). Mirrors the
// BuildsHere() && !RemoteWorktree() pair in erun-common.
export function environmentTypeBuildsHereLocally(type: string | undefined): boolean {
  return type === 'local-agent';
}

// environmentTypeBuildsHere reports whether the env builds its own runtime
// image rather than consuming a published one — local-agent, remote-agent,
// and host all build here; only runtime consumes a published version. An
// empty/unset type is treated as building so an unresolved env keeps today's
// behaviour. Enumerated explicitly (rather than `type !== 'runtime'`) so a
// fifth type is a deliberate decision here, not a default that happens to
// land on the right answer for host by coincidence. Mirrors
// EnvConfig.BuildsHere() in erun-common/config.go.
export function environmentTypeBuildsHere(type: string | undefined): boolean {
  return (
    type === 'local-agent' ||
    type === 'remote-agent' ||
    type === 'host' ||
    type === undefined ||
    type === ''
  );
}
