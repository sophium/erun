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

// HostedDefinitionSection is the environment's hosted marker panel: which
// platform row this local environment corresponds to, whether this machine's
// own settings have moved since they were last sent, and — on request — how far
// the local copy has fallen behind the definition that row holds.
//
// The divergence half needs no platform read, so it is rendered straight from
// the read model: a change made while the desktop was closed (`erun init`,
// `erun cloud set`, a deploy) is visible here without asking anything. The
// desktop also uploads such a change on its own; the button is what the
// operator has when it could not — a machine that is not signed in to the
// tenant's platform, or an upload that failed.
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
  // The comparison is about one specific environment. Switching to another one
  // in the same dialog must not leave the previous environment's answer on
  // screen, so everything resolved is dropped whenever the target changes.
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

  // A completed upload reports the divergence it read back from disk, which is
  // more current than the marker this panel was rendered with.
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
    // A named region rather than a bare div: the panel is a self-contained
    // group of facts about this environment, and naming it is what lets a
    // test -- or a screen reader's landmark list -- address the panel itself
    // rather than whichever descendant happens to carry a role.
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

// HostedDefinitionLocalChangeLine renders whether this machine's settings have
// moved since they were last sent.
//
// The action is offered only where an upload would do something: a copy in step
// needs no button, and one this desktop cannot tell about needs the upload that
// starts tracking it as much as a changed one does. A failed upload is an
// attempted failure and reads as an alert, per the shared design-language
// record; a divergence and the unknown state are status.
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
      {/* One status line for this machine's own settings, whether it is
          reporting the standing divergence or the upload that just answered it.
          Two would both be live regions about the same subject, and the named
          region is what lets a reader — or a test — address this line rather
          than whichever status the panel happens to render first. */}
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

// HostedDefinitionDriftLine renders the resolved comparison, or nothing at all
// when no comparison has been made — absence reads as "nothing to say", which
// is not the same as "up to date", so it must not be rendered as one.
function HostedDefinitionDriftLine({
  drift,
}: {
  drift?: UIHostedDefinitionDrift;
}): React.ReactElement | null {
  if (!drift) {
    return null;
  }
  if (drift.error) {
    // An attempted failure is an alert, per the shared design-language record.
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
      {/* The recovery action names the command that performs it: the desktop
          does not pull on its own, and offering a button that does nothing
          would be worse than naming the one that works. */}
      <span>{drift.describe} — run `erun platform env pull` to bring it down.</span>
    </div>
  );
}
