import { Button } from 'erun-kit';
import { Copy } from 'lucide-react';
import * as React from 'react';

import { accessRemedyFor } from '@/app/accessRemedies';
import { useAppDispatch } from '@/app/hooks';
import { showNotification } from '@/app/notificationThunks';
import type { UIAccessRemedy } from '@/types';

// AccessRemedyNote renders the copyable grant command for an administrator; the
// backend omits it when no role covers the missing access.
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
        {/* The sentence above already asks for an administrator. */}
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

// AccessDeniedBody is the shared body of every capability denial.
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
