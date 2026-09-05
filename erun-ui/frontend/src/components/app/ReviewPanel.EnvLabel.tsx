import * as React from 'react';

// ReviewEnvLabel is the one label treatment shared by the review-layers block
// (ReviewPanel.tsx) and the changed-files tree section (ReviewPanel.ChangedFiles.tsx)
// for the same environment (#1314): both render this exact component when
// more than one environment is in scope, so the two read as one group instead
// of two independently-labelled lists. "tenant / environment" (spaced) reads
// as the app's existing environment presentation, matching the format
// sidebar/aria-label text already uses, rather than the raw
// `tenant/environment` envKey.
export function ReviewEnvLabel({
  tenant,
  environment,
}: {
  tenant: string;
  environment: string;
}): React.ReactElement {
  return (
    <div className="min-w-0 truncate text-sm font-semibold text-foreground">
      {tenant} / {environment}
    </div>
  );
}
