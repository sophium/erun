import { ExternalLink, GitFork } from 'lucide-react';
import * as React from 'react';

import { toggleContribute } from '@/app/contributeThunks';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { showNotification } from '@/app/notificationThunks';
import { contributeEnvKey } from '@/app/slices/contributeSlice';
import { IconTooltip } from '@/components/app/IconTooltip';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import type { UISelection } from '@/types';

import { StartContributeApp } from '../../../wailsjs/go/main/App';

const titlebarButtonClassName =
  'size-7 flex-none cursor-pointer rounded-[var(--radius)] border-0 bg-transparent text-muted-foreground [--wails-draggable:no-drag] hover:bg-accent hover:text-accent-foreground [&_svg]:size-[18px]';

const activeTitlebarButtonClassName =
  'bg-primary text-primary-foreground hover:bg-primary hover:text-primary-foreground';

// ContributeToggle renders the per-env Contribute switch + companion
// launcher in the titlebar. Visible only for non-erun envs whose
// configured type is local-agent or remote-agent. Toggling on clones
// ERun inside the env and spawns the two contribute tabs; toggling off
// closes them. While on, an adjacent ExternalLink button boots the
// headless `erun app` inside the contribute terminal, brings up a
// kubectl port-forward, and opens the locally-built desktop app in
// the user's default browser.
export function ContributeToggle(): React.ReactElement | null {
  const selected = useAppSelector((state) => state.selection.selected);
  const tenants = useAppSelector((state) => state.tenants.tenants);
  if (!selected) return null;
  if (selected.tenant.toLowerCase() === 'erun') return null;
  const tenant = tenants.find((t) => t.name === selected.tenant);
  const env = tenant?.environments.find((e) => e.name === selected.environment);
  if (!env) return null;
  const type = (env.type ?? '').toLowerCase();
  if (type !== 'local-agent' && type !== 'remote-agent') return null;
  return <ContributeToggleControls selected={selected} />;
}

function ContributeToggleControls({ selected }: { selected: UISelection }): React.ReactElement {
  const flag = useAppSelector((state) =>
    Boolean(state.contribute.flagsByEnv[contributeEnvKey(selected.tenant, selected.environment)]),
  );
  return (
    <>
      <ContributeToggleButton selected={selected} flag={flag} />
      {flag && <ContributeAppLauncher selected={selected} />}
    </>
  );
}

function ContributeToggleButton({
  selected,
  flag,
}: {
  selected: UISelection;
  flag: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const [busy, setBusy] = React.useState(false);
  const label = flag ? 'Disable contribute mode' : 'Contribute to ERun';
  return (
    <IconTooltip label={label}>
      <Button
        className={cn(titlebarButtonClassName, flag && activeTitlebarButtonClassName)}
        type="button"
        variant="ghost"
        size="icon"
        aria-label={label}
        aria-pressed={flag}
        disabled={busy}
        onClick={() => {
          if (busy) return;
          setBusy(true);
          void dispatch(toggleContribute(selected)).finally(() => {
            setBusy(false);
          });
        }}
      >
        <GitFork />
      </Button>
    </IconTooltip>
  );
}

function ContributeAppLauncher({ selected }: { selected: UISelection }): React.ReactElement {
  const dispatch = useAppDispatch();
  const [busy, setBusy] = React.useState(false);
  const label = 'Open contribute app in browser';
  return (
    <IconTooltip label={label}>
      <Button
        className={titlebarButtonClassName}
        type="button"
        variant="ghost"
        size="icon"
        aria-label={label}
        disabled={busy}
        onClick={() => {
          if (busy) return;
          setBusy(true);
          // The Go side opens the URL in the user's default browser
          // via Wails BrowserOpenURL after the headless server is
          // actually serving HTTP; the frontend only needs to surface
          // the URL in the notification so the user can copy/paste if
          // their default browser handler is misconfigured.
          StartContributeApp(selected)
            .then((launch) => {
              if (launch.url) {
                dispatch(showNotification('success', `Contribute app ready at ${launch.url}`));
              }
            })
            .catch((e: unknown) => {
              const message = e instanceof Error ? e.message : String(e);
              dispatch(showNotification('error', `Failed to open contribute app: ${message}`));
            })
            .finally(() => {
              setBusy(false);
            });
        }}
      >
        <ExternalLink />
      </Button>
    </IconTooltip>
  );
}
