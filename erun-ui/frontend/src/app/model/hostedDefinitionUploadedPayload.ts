// HostedDefinitionUploadedPayload refreshes the Manage dialog's marker, which the
// environments-changed reload does not touch.
export interface HostedDefinitionUploadedPayload {
  tenant: string;
  environment: string;
  revision: number;
}
