import { TriangleAlert } from 'lucide-react';
import * as React from 'react';

import { versionNoticeMessage } from '@/app/versionSuggestions';
import type { UIVersionSuggestionNotice } from '@/types';

// VersionNotices renders, below the version choices, why a runtime-image source
// contributed none — so a private or unreachable image reads as an actionable
// state rather than a silently empty list (visibility of system status).
export function VersionNotices({
  notices,
}: {
  notices: UIVersionSuggestionNotice[];
}): React.ReactElement | null {
  if (notices.length === 0) {
    return null;
  }
  return (
    <ul
      className="flex flex-col gap-1.5 border-t border-border px-3 py-2"
      aria-label="Version source notices"
    >
      {notices.map((notice) => (
        <li
          key={`${notice.image}:${notice.kind}`}
          className="flex items-start gap-2 text-xs leading-snug text-muted-foreground"
        >
          <TriangleAlert aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-amber-600" />
          <span className="[overflow-wrap:anywhere]">{versionNoticeMessage(notice)}</span>
        </li>
      ))}
    </ul>
  );
}
