// Hosted-definition marker types, mirroring erun-ui/hosted_definition.go.

export interface UIHostedEnvironment {
  describe: string;
  apiHost: string;
  tenantId: string;
  environmentId: string;
  // Zero means never pulled on this machine.
  definitionRevision: number;
  // Resolved from local config alone, so always present.
  localChange: UIHostedDefinitionLocalChange;
  // Absent means not compared yet, not "up to date".
  drift?: UIHostedDefinitionDrift;
}

// available is false when no digest is recorded: "cannot tell", never "in step".
export interface UIHostedDefinitionLocalChange {
  available: boolean;
  changed: boolean;
  describe: string;
}

export interface UIHostedDefinitionDrift {
  localRevision: number;
  platformRevision: number;
  behind: boolean;
  describe: string;
  error?: string;
}

// Local divergence is read back from disk after the transfer.
export interface UIHostedDefinitionUpload {
  revision: number;
  describe: string;
  localChange: UIHostedDefinitionLocalChange;
}
