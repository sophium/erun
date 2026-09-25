// The hosted-definition marker types, mirroring the Go read model in
// erun-ui/hosted_definition.go. They live in their own file for the same
// reason the other per-domain type files do: types.ts is the shared surface,
// and a domain that only two components read does not belong in it.

// UIHostedEnvironment mirrors the Go uiHostedEnvironment: which platform row
// this local environment corresponds to, and how far the local copy has fallen
// behind the definition that row holds. The drift half is read-only and is
// resolved on demand — the desktop never uploads on a config change.
export interface UIHostedEnvironment {
  describe: string;
  apiHost: string;
  tenantId: string;
  environmentId: string;
  // Zero means this machine has never pulled this environment's definition.
  definitionRevision: number;
  // Absent until a comparison has been made: absent reads as "nothing to say",
  // which is not the same as "up to date".
  drift?: UIHostedDefinitionDrift;
}

// UIHostedDefinitionDrift is the marker panel's own refresh result. error is
// set when the comparison could not be made, and states what could not be read
// before the cause.
export interface UIHostedDefinitionDrift {
  localRevision: number;
  platformRevision: number;
  behind: boolean;
  describe: string;
  error?: string;
}
