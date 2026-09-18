import * as React from 'react';

import type { ReviewTarget } from '@/app/selectors';

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

// ReviewTargetLabel is that same treatment for either kind of target: an
// environment's "tenant / environment", or a directory's path. A directory has
// no tenant or environment to render, and showing its path is what tells the
// operator which checkout this section is, so the two read as one group without
// one pretending to be the other.
export function ReviewTargetLabel({ target }: { target: ReviewTarget }): React.ReactElement {
  if (target.kind === 'directory') {
    return (
      <div
        className="min-w-0 truncate font-mono text-sm font-semibold text-foreground"
        title={target.directory}
      >
        {target.directory}
      </div>
    );
  }
  return <ReviewEnvLabel tenant={target.tenant} environment={target.environment} />;
}
