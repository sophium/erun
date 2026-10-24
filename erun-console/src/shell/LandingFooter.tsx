import type * as React from 'react';

import { PUBLIC_DOCS_URL } from './landingContent';

// Falls back to the public docs site so the footer always has a docs link.
export function LandingFooter({
  brandLabel,
  docsUrl,
}: {
  brandLabel: string;
  docsUrl: string | undefined;
}): React.ReactElement {
  const resolvedDocsUrl = docsUrl !== undefined && docsUrl.length > 0 ? docsUrl : PUBLIC_DOCS_URL;
  return (
    <footer className="border-t border-border px-6 py-8">
      <div className="mx-auto flex max-w-5xl flex-col items-center justify-between gap-3 text-sm text-muted-foreground sm:flex-row">
        <span>{brandLabel}</span>
        <a
          href={resolvedDocsUrl}
          target="_blank"
          rel="noreferrer"
          className="font-medium text-foreground underline-offset-4 hover:underline"
        >
          Read the docs
        </a>
      </div>
    </footer>
  );
}
