// HostedDefinitionUploadedPayload is the Wails event the desktop fires when a
// watcher-driven upload writes a new revision for one environment.
//
// The upload's own config write also fires environments-changed, but that tick
// reloads the sidebar and the tenant list — not the Manage dialog's environment
// config, which is what renders the marker row and the local divergence.
// Without this the dialog would keep showing the revision the environment had
// before the upload, in the one dialog whose subject is that revision.
export interface HostedDefinitionUploadedPayload {
  tenant: string;
  environment: string;
  revision: number;
}
