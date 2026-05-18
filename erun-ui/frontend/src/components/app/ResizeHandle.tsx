import * as React from 'react';

import { cn } from '@/lib/utils';

interface ResizeHandleProps {
  className?: string;
  orientation: 'vertical' | 'horizontal';
  label: string;
  hidden?: boolean;
  onMouseDown: (event: React.MouseEvent<HTMLButtonElement>) => void;
}

// ResizeHandle renders a focusable resize handle as a real button so the
// jsx-a11y interactive rules are satisfied without an inline disable.
// The orientation is exposed via data-orientation for styling and for
// assistive-tech testing hooks; keyboard resizing is a follow-up.
export function ResizeHandle({
  className,
  orientation,
  label,
  hidden,
  onMouseDown,
}: ResizeHandleProps): React.ReactElement {
  return (
    <button
      type="button"
      className={cn(className, hidden && 'pointer-events-none hidden')}
      aria-label={label}
      data-orientation={orientation}
      onMouseDown={onMouseDown}
    />
  );
}
