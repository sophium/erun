import * as React from 'react';

import { InlineAlert } from '@/components/app/InlineAlert';

// RuntimePanelNotice is the Runtime tab's one rendering for "the probe could
// not read this environment". All three panels report that same failed read,
// so they render it through the inline family's member for an attempted
// failure -- InlineAlert, which carries role="alert" so the failure is
// announced and an icon so it is not signalled by colour alone -- rather than
// each panel rolling its own tone (amber with an icon, amber without, muted).
//
// A panel's own empty state ("nothing to report yet") is not a failure and
// stays plain role="status" text, which the decision record sanctions for an
// inline result.
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
