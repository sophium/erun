import { Card } from 'erun-kit';
import type * as React from 'react';

import { DIFFERENTIATORS } from './landingContent';

// A row of four cards, not the docs site's hand-drawn diagram: the diagram's
// charcoal/cyan vocabulary is a different visual system than the console's
// shadcn/Tailwind tokens, and a copy loaded as a static <img> would not adapt
// to the console's dark-mode toggle the way these cards do for free (#1327).
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
          {DIFFERENTIATORS.map((item) => (
            <Card key={item.key} className="flex flex-col gap-3 px-6">
              <item.icon aria-hidden="true" className="size-6 text-primary" />
              <h3 className="text-base font-semibold text-card-foreground">{item.label}</h3>
              <p className="text-sm text-muted-foreground">{item.description}</p>
            </Card>
          ))}
        </div>
      </div>
    </section>
  );
}
