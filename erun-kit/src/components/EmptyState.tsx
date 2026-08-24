import * as React from 'react';

import { cn } from '../lib/utils';

export function EmptyState({
  icon,
  heading,
  body,
  action,
  className,
}: {
  icon?: React.ReactElement;
  heading: string;
  body?: React.ReactNode;
  action?: React.ReactNode;
  className?: string;
}): React.ReactElement {
  return (
    <div
      className={cn(
        'grid gap-2 rounded-[var(--radius)] border border-dashed border-border bg-muted/20 px-4 py-5 text-center',
        className,
      )}
      role="status"
    >
      {icon && (
        <div
          className="flex justify-center text-muted-foreground [&_svg]:size-5"
          aria-hidden="true"
        >
          {icon}
        </div>
      )}
      <div className="text-sm font-medium text-foreground">{heading}</div>
      {body && <div className="text-[13px] leading-[1.4] text-muted-foreground">{body}</div>}
      {action && <div className="flex justify-center pt-1">{action}</div>}
    </div>
  );
}
