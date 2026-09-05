import { Card, cn } from 'erun-kit';
import type * as React from 'react';

import { DIFFERENTIATORS } from './landingContent';

const FEATURED_INDEX = 0;
const BANNER_INDEX = 3;

// Four identical outline boxes read as a template, not a designed page.
// Give the set rhythm instead — the strongest claim ("Side by side", the
// product's one distinctive interaction model) takes the accent treatment
// and double width, and the last card runs full-width as a closing banner,
// so a reader has somewhere to look first rather than scanning four equals
// left to right. Cards, not the docs site's hand-drawn diagram: a copy
// loaded as a static image wouldn't repaint for the dark-mode toggle.
function cardClassName(index: number): string {
  if (index === FEATURED_INDEX) {
    return 'border-transparent bg-accent-brand text-accent-brand-foreground lg:col-span-2';
  }
  if (index === BANNER_INDEX) {
    return 'lg:col-span-4 lg:flex-row lg:items-center lg:gap-6';
  }
  return '';
}

function iconClassName(index: number): string {
  if (index === FEATURED_INDEX) {
    return 'size-8 text-accent-brand-foreground';
  }
  if (index === BANNER_INDEX) {
    return 'size-6 shrink-0 text-accent-brand';
  }
  return 'size-6 text-accent-brand';
}

export function LandingDifferentiators({ brandLabel }: { brandLabel: string }): React.ReactElement {
  return (
    <section aria-labelledby="differentiators-heading" className="px-6 py-16 sm:py-20">
      <div className="mx-auto max-w-5xl">
        <h2
          id="differentiators-heading"
          className="text-center text-2xl font-semibold text-foreground sm:text-3xl"
        >
          What makes {brandLabel} different
        </h2>
        <div className="mt-10 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {DIFFERENTIATORS.map((item, index) => (
            <Card
              key={item.key}
              style={{ animationDelay: `${String(index * 100)}ms` }}
              className={cn(
                'flex flex-col gap-3 px-6 motion-safe:animate-in motion-safe:fade-in motion-safe:slide-in-from-bottom-2 motion-safe:duration-500 motion-safe:fill-mode-both',
                cardClassName(index),
              )}
            >
              <item.icon aria-hidden="true" className={iconClassName(index)} />
              <h3 className="text-base font-semibold">{item.label}</h3>
              <p
                className={cn(
                  'text-sm',
                  index === FEATURED_INDEX
                    ? 'text-accent-brand-foreground/80'
                    : 'text-muted-foreground',
                )}
              >
                {item.description}
              </p>
            </Card>
          ))}
        </div>
      </div>
    </section>
  );
}
