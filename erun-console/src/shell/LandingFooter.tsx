import type * as React from 'react';

export function LandingFooter({
  brandLabel,
  docsUrl,
}: {
  brandLabel: string;
  docsUrl: string | undefined;
}): React.ReactElement {
  const hasDocsUrl = docsUrl !== undefined && docsUrl.length > 0;
  return (
    <footer className="border-t border-border px-6 py-8">
      <div className="mx-auto flex max-w-5xl flex-col items-center justify-between gap-3 text-sm text-muted-foreground sm:flex-row">
        <span>{brandLabel}</span>
        {hasDocsUrl && (
          <a
            href={docsUrl}
            target="_blank"
            rel="noreferrer"
            className="font-medium text-foreground underline-offset-4 hover:underline"
          >
            Read the docs
          </a>
        )}
      </div>
    </footer>
  );
}
