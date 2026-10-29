import { Button, Label } from 'erun-kit';
import { CloudDownload, RefreshCw, Upload } from 'lucide-react';
import * as React from 'react';

import { readError } from '@/app/errors';
import { readHostedDefinitionDrift, uploadHostedDefinition } from '@/app/hostedDefinitionDrift';
import type {
  UIHostedDefinitionDrift,
  UIHostedDefinitionLocalChange,
  UIHostedDefinitionUpload,
  UIHostedEnvironment,
} from '@/uiHostedDefinitionTypes';

// HostedDefinitionSection renders divergence from the read model without a
// platform read; the upload button covers cases the desktop's own upload could not.
export function HostedDefinitionSection({
  tenant,
  environment,
  hosted,
}: {
  tenant: string;
  environment: string;
  hosted?: UIHostedEnvironment;
}): React.ReactElement | null {
  const [drift, setDrift] = React.useState<UIHostedDefinitionDrift | undefined>(undefined);
  const [busy, setBusy] = React.useState(false);
  const [upload, setUpload] = React.useState<UIHostedDefinitionUpload | undefined>(undefined);
  const [uploadError, setUploadError] = React.useState<string | undefined>(undefined);
  const [uploading, setUploading] = React.useState(false);
  // Drop resolved state when the target environment changes.
  React.useEffect(() => {
    setDrift(undefined);
    setBusy(false);
    setUpload(undefined);
    setUploadError(undefined);
    setUploading(false);
  }, [tenant, environment]);

  if (!hosted) {
    return null;
  }

  // An upload's read-back divergence is more current than the rendered marker.
  const localChange = upload?.localChange ?? hosted.localChange;

  const checkDrift = async (): Promise<void> => {
    setBusy(true);
    try {
      setDrift(await readHostedDefinitionDrift(tenant, environment));
    } finally {
      setBusy(false);
    }
  };

  const sendToPlatform = async (): Promise<void> => {
    setUploading(true);
    setUploadError(undefined);
    try {
      setUpload(await uploadHostedDefinition(tenant, environment));
    } catch (error: unknown) {
      setUploadError(readError(error));
    } finally {
      setUploading(false);
    }
  };

  return (
    <section
      aria-label="Hosted environment"
      className="grid gap-3 rounded-[var(--radius)] border border-border px-3 py-3"
    >
      <div className="flex items-center justify-between gap-3">
        <Label className="text-sm font-medium">Hosted environment</Label>
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={busy}
          onClick={() => void checkDrift()}
          aria-label="Check whether the platform's copy of this environment has changed"
        >
          <RefreshCw className="mr-2 h-4 w-4" aria-hidden="true" />
          Check for updates
        </Button>
      </div>
      <div className="text-sm text-muted-foreground [overflow-wrap:anywhere]">
        {hosted.describe}
      </div>
      <HostedDefinitionLocalChangeLine
        change={localChange}
        error={uploadError}
        uploaded={upload?.describe}
        uploading={uploading}
        retryCommand={`erun platform env push ${tenant} ${environment}`}
        onUpload={() => void sendToPlatform()}
      />
      <HostedDefinitionDriftLine drift={drift} />
    </section>
  );
}

// The upload action appears only where an upload would do something; a failed
// upload is an alert, divergence and unknown are status.
function HostedDefinitionLocalChangeLine({
  change,
  error,
  uploaded,
  uploading,
  retryCommand,
  onUpload,
}: {
  change: UIHostedDefinitionLocalChange;
  error?: string;
  uploaded?: string;
  uploading: boolean;
  retryCommand: string;
  onUpload: () => void;
}): React.ReactElement {
  const actionable = !change.available || change.changed;
  return (
    <div className="grid gap-2">
      {/* One live region for this machine's settings, so the two never compete. */}
      <div
        role="status"
        aria-label="This machine's settings"
        className="text-sm text-muted-foreground [overflow-wrap:anywhere]"
      >
        {change.describe}
        {uploaded ? <span> {uploaded}</span> : null}
      </div>
      {error ? (
        <div role="alert" className="text-sm text-amber-600 [overflow-wrap:anywhere]">
          Cannot upload this environment&apos;s settings: {error} — retry here, or run `
          {retryCommand}` from a terminal.
        </div>
      ) : null}
      {actionable ? (
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="w-fit"
          disabled={uploading}
          onClick={onUpload}
          aria-label="Upload this environment's settings to the platform"
        >
          <Upload className="mr-2 h-4 w-4" aria-hidden="true" />
          {uploading ? 'Uploading…' : 'Upload to platform'}
        </Button>
      ) : null}
    </div>
  );
}

// Renders nothing when no comparison was made: absence is not "up to date".
function HostedDefinitionDriftLine({
  drift,
}: {
  drift?: UIHostedDefinitionDrift;
}): React.ReactElement | null {
  if (!drift) {
    return null;
  }
  if (drift.error) {
    return (
      <div role="alert" className="text-sm text-amber-600 [overflow-wrap:anywhere]">
        {drift.error}
      </div>
    );
  }
  if (!drift.behind) {
    return (
      <div
        role="status"
        aria-label="The platform's definition"
        className="text-sm text-muted-foreground"
      >
        {drift.describe}
      </div>
    );
  }
  return (
    <div
      role="status"
      aria-label="The platform's definition"
      className="flex items-center gap-2 text-sm text-muted-foreground"
    >
      <CloudDownload className="h-4 w-4 shrink-0" aria-hidden="true" />
      {/* The desktop does not pull on its own, so name the command. */}
      <span>{drift.describe} — run `erun platform env pull` to bring it down.</span>
    </div>
  );
}
