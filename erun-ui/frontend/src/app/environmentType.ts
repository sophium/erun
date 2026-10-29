// `type` is the single source of truth for an env's shape; these classifiers
// must stay in sync with the deciders in erun-common/config.go.

// environmentTypeIsRemoteWorktree treats an unset type as local.
export function environmentTypeIsRemoteWorktree(type: string | undefined): boolean {
  return type === 'remote-agent' || type === 'runtime';
}

// environmentTypeIsHost reports a pod-less directory env on this machine.
export function environmentTypeIsHost(type: string | undefined): boolean {
  return type === 'host';
}

// environmentTypeIsRuntime is the only type that can mount a source worktree.
export function environmentTypeIsRuntime(type: string | undefined): boolean {
  return type === 'runtime';
}

// environmentTypeBuildsHereLocally reports a local-agent env, the only kind
// that can build and deploy a new version from the desktop.
export function environmentTypeBuildsHereLocally(type: string | undefined): boolean {
  return type === 'local-agent';
}

// environmentTypeBuildsHere treats an unset type as building. Types are listed
// explicitly so a new type is a deliberate decision.
export function environmentTypeBuildsHere(type: string | undefined): boolean {
  return (
    type === 'local-agent' ||
    type === 'remote-agent' ||
    type === 'host' ||
    type === undefined ||
    type === ''
  );
}
