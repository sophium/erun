import * as React from 'react';

import { InlineAlert } from '@/components/app/InlineAlert';

// RuntimePanelNotice is the Runtime tab's one rendering for a failed probe read; empty states stay plain status text.
export function RuntimePanelNotice({
  failure,
  empty,
}: {
  failure: string;
  empty: string;
}): React.ReactElement {
  if (failure) {
    return <InlineAlert>{failure}</InlineAlert>;
  }
  return (
    <p className="text-xs leading-[1.35] text-muted-foreground" role="status">
      {empty}
    </p>
  );
}
