import { Button, Label } from 'erun-kit';
import { CloudDownload, RefreshCw } from 'lucide-react';
import * as React from 'react';

import { readHostedDefinitionDrift } from '@/app/hostedDefinitionDrift';
import type { UIHostedDefinitionDrift, UIHostedEnvironment } from '@/uiHostedDefinitionTypes';

// HostedDefinitionSection is the environment's hosted marker panel: which
// platform row this local environment corresponds to, and — on request — how
// far the local copy has fallen behind the definition that row holds.
//
// It is deliberately read-only and never writes. There is no auto-upload
// control here: driving an upload from the config watcher is the write→event→
// write loop this repository has already shipped once (see
// erun-ui/AGENTS.md), and the dirty-flag half of that design is not built.
// The operator pulls from the CLI (`erun platform env pull`); this panel tells
// them whether there is anything to pull.
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
  // The comparison is about one specific environment. Switching to another one
  // in the same dialog must not leave the previous environment's answer on
  // screen, so the resolved drift is dropped whenever the target changes.
  React.useEffect(() => {
    setDrift(undefined);
    setBusy(false);
  }, [tenant, environment]);

  if (!hosted) {
    return null;
  }

  const checkDrift = async (): Promise<void> => {
    setBusy(true);
    try {
      setDrift(await readHostedDefinitionDrift(tenant, environment));
    } finally {
      setBusy(false);
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
      <HostedDefinitionDriftLine drift={drift} />
    </section>
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
    return <div className="text-sm text-muted-foreground">{drift.describe}</div>;
  }
  return (
    <div role="status" className="flex items-center gap-2 text-sm text-muted-foreground">
      <CloudDownload className="h-4 w-4 shrink-0" aria-hidden="true" />
      {/* The recovery action names the command that performs it: the desktop
          does not pull on its own, and offering a button that does nothing
          would be worse than naming the one that works. */}
      <span>{drift.describe} — run `erun platform env pull` to bring it down.</span>
    </div>
  );
}
