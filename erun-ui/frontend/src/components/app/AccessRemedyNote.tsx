import { Button } from 'erun-kit';
import { Copy } from 'lucide-react';
import * as React from 'react';

import { accessRemedyFor } from '@/app/accessRemedies';
import { useAppDispatch } from '@/app/hooks';
import { showNotification } from '@/app/notificationThunks';
import type { UIAccessRemedy } from '@/types';

// AccessRemedyNote renders the "hand this to an administrator" half of a
// denial — the same next step the not-enrolled screen already gives, applied
// to an access the caller is missing rather than an enrollment they lack. The
// command is complete as rendered: it carries this caller's own user id, so
// copying it is the whole of the work the operator has left to do.
//
// The backend omits a remedy entirely when no role covers the missing access
// or the tenant's roles could not be read, which is why it is optional here
// and absent renders nothing at all.
export function AccessRemedyNote({
  remedy,
}: {
  remedy?: UIAccessRemedy;
}): React.ReactElement | null {
  const dispatch = useAppDispatch();
  const command = remedy?.command;
  if (!command) {
    return null;
  }
  return (
    <div className="mt-2 grid gap-1">
      <div className="flex flex-wrap items-center justify-center gap-2">
        {/* The sentence above already says to ask an administrator, so this
            half is the artifact itself rather than a second request for one. */}
        <span>
          {remedy.roleName
            ? `Send an administrator the command that grants you ${remedy.roleName}:`
            : 'Send an administrator the command that grants it:'}
        </span>
        <Button
          type="button"
          variant="outline"
          size="sm"
          aria-label="Copy grant command"
          onClick={() => {
            void navigator.clipboard.writeText(command).then(() => {
              dispatch(showNotification('success', 'Copied the grant command.'));
            });
          }}
        >
          <Copy aria-hidden="true" />
          Copy command
        </Button>
      </div>
      <code className="rounded bg-muted px-2 py-1 text-[12px] break-all">{command}</code>
    </div>
  );
}

// AccessDeniedBody is the shared body of a capability denial: the sentence
// naming what is missing, plus the copyable grant when the backend resolved
// one. Every denial that says "It needs X. Ask an administrator for access."
// renders through here, so all of them hand over the request together rather
// than one at a time.
export function AccessDeniedBody({
  remedies,
  restricted,
  children,
}: {
  remedies?: Record<string, UIAccessRemedy>;
  restricted?: string;
  children: React.ReactNode;
}): React.ReactElement {
  return (
    <>
      <p>{children}</p>
      <AccessRemedyNote remedy={accessRemedyFor(remedies, restricted)} />
    </>
  );
}
