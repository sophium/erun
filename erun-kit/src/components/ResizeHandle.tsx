import * as React from 'react';

import { cn } from '../lib/utils';

interface ResizeHandleProps {
  className?: string;
  orientation: 'vertical' | 'horizontal';
  label: string;
  hidden?: boolean;
  onMouseDown: (event: React.MouseEvent<HTMLButtonElement>) => void;
}

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
