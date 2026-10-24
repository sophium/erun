import * as React from 'react';

import type { ReviewTarget } from '@/app/selectors';

// ReviewEnvLabel is shared by the layers block and the changed-files tree so they read as one group.
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

// ReviewTargetLabel shows "tenant / environment" or a directory's path.
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
