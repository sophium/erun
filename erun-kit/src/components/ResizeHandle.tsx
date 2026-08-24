import * as React from 'react';

import { cn } from '../lib/utils';

export const RESIZE_STEP = 16;
export const RESIZE_STEP_LARGE = 64;

interface ResizeHandleValue {
  now: number;
  min: number;
  max: number;
}

interface ResizeHandleProps {
  className?: string;
  orientation: 'vertical' | 'horizontal';
  label: string;
  hidden?: boolean;
  onMouseDown: (event: React.MouseEvent<HTMLDivElement>) => void;
  value: ResizeHandleValue;
  onStep: (delta: number) => void;
}

// role="slider" (rather than the visually-closer "separator") is what lets
// this carry aria-valuenow/min/max/orientation and mouse+keyboard handlers on
// a plain element without a native form control -- a resize handle is, after
// all, a one-dimensional control over a pixel value with a min and a max. A
// vertical handle is a vertical divider line: it separates panes side-by-side,
// so its keyboard axis is left/right. A horizontal handle stacks panes
// top/bottom, so its axis is up/down. In both cases the "positive" arrow
// (Right/Down) increases the value this handle reports.
export function ResizeHandle({
  className,
  orientation,
  label,
  hidden,
  onMouseDown,
  value,
  onStep,
}: ResizeHandleProps): React.ReactElement {
  const increaseKey = orientation === 'vertical' ? 'ArrowRight' : 'ArrowDown';
  const decreaseKey = orientation === 'vertical' ? 'ArrowLeft' : 'ArrowUp';
  const handleKeyDown = (event: React.KeyboardEvent<HTMLDivElement>): void => {
    const step = event.shiftKey ? RESIZE_STEP_LARGE : RESIZE_STEP;
    if (event.key === increaseKey) {
      event.preventDefault();
      onStep(step);
    } else if (event.key === decreaseKey) {
      event.preventDefault();
      onStep(-step);
    }
  };
  return (
    <div
      role="slider"
      tabIndex={0}
      aria-orientation={orientation}
      aria-valuenow={Math.round(value.now)}
      aria-valuemin={Math.round(value.min)}
      aria-valuemax={Math.round(value.max)}
      className={cn(
        className,
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1',
        hidden && 'pointer-events-none hidden',
      )}
      aria-label={label}
      data-orientation={orientation}
      onMouseDown={onMouseDown}
      onKeyDown={handleKeyDown}
    />
  );
}
