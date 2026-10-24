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

// role="slider" allows aria-value* on a plain element. Vertical handles use Left/Right,
// horizontal Up/Down; Right/Down increase the value.
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
