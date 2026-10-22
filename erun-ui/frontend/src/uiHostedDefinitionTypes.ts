// The hosted-definition marker types, mirroring the Go read model in
// erun-ui/hosted_definition.go. They live in their own file for the same
// reason the other per-domain type files do: types.ts is the shared surface,
// and a domain that only two components read does not belong in it.

// UIHostedEnvironment mirrors the Go uiHostedEnvironment: which platform row
// this local environment corresponds to, how far the local copy has fallen
// behind the definition that row holds, and whether this machine's own
// settings have moved since they were last sent.
export interface UIHostedEnvironment {
  describe: string;
  apiHost: string;
  tenantId: string;
  environmentId: string;
  // Zero means this machine has never pulled this environment's definition.
  definitionRevision: number;
  // localChange is resolved from the local config alone, so it is always
  // present — including on a machine that has never reached the platform.
  localChange: UIHostedDefinitionLocalChange;
  // Absent until a comparison has been made: absent reads as "nothing to say",
  // which is not the same as "up to date".
  drift?: UIHostedDefinitionDrift;
}

// UIHostedDefinitionLocalChange is whether this machine's portable settings
// have moved since the marker last recorded a transfer. available is false when
// no digest is recorded — a marker written before this machine tracked one —
// and that reads as "cannot tell", never as "in step".
export interface UIHostedDefinitionLocalChange {
  available: boolean;
  changed: boolean;
  describe: string;
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

// UIHostedDefinitionUpload is what an upload did: the revision it wrote, the
// sentence naming it, and the local divergence as of the upload — read back
// from disk, so a copy that changed underneath the transfer still reads as
// changed rather than as synchronised.
export interface UIHostedDefinitionUpload {
  revision: number;
  describe: string;
  localChange: UIHostedDefinitionLocalChange;
}
