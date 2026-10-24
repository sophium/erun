import { Button } from 'erun-kit';
import { Bug, CheckCircle2, ClipboardList } from 'lucide-react';
import * as React from 'react';

import {
  buildDiagnosticsIssueURL,
  diagnosticsIssueBody,
  diagnosticsIssueTitle,
} from '@/app/diagnosticsIssue';
import { useDiagnosticsReportAssembly } from '@/app/diagnosticsReportAssembly';
import type { DiagnosticsContext } from '@/app/selectors';

import { BrowserOpenURL, ClipboardSetText } from '../../../wailsjs/runtime/runtime';

// Both report actions share useDiagnosticsReportAssembly so their evidence matches.

// CopyReportButton re-reads the evidence rather than reusing a polled copy.
export function CopyReportButton({ context }: { context: DiagnosticsContext }): React.ReactElement {
  const { assemble } = useDiagnosticsReportAssembly(context);
  const [status, setStatus] = React.useState('');

  const copyReport = React.useCallback(() => {
    assemble()
      .then(({ report }) => ClipboardSetText(report))
      .then(() => {
        setStatus('Copied');
        window.setTimeout(() => {
          setStatus('');
        }, 1400);
      })
      .catch(() => {
        setStatus('Copy failed');
      });
  }, [assemble]);

  return (
    <Button
      className="h-6 px-2 text-[11px] [&_svg]:size-3.5"
      type="button"
      variant="ghost"
      size="sm"
      onClick={copyReport}
    >
      {status === 'Copied' ? (
        <CheckCircle2 aria-hidden="true" />
      ) : (
        <ClipboardList aria-hidden="true" />
      )}
      {status || 'Copy report'}
    </Button>
  );
}

// ReportIssueButton opens a prefilled issue in the browser (the desktop has no
// GitHub token) and also copies the untruncated report, since the URL may trim it.
export function ReportIssueButton({
  context,
}: {
  context: DiagnosticsContext;
}): React.ReactElement {
  const { build, assemble } = useDiagnosticsReportAssembly(context);
  const [status, setStatus] = React.useState('');

  const reportIssue = React.useCallback(() => {
    assemble()
      .then(async ({ report, reportContext }) => {
        const title = diagnosticsIssueTitle(reportContext);
        const body = diagnosticsIssueBody(report, build);
        const { url } = buildDiagnosticsIssueURL(title, body);
        BrowserOpenURL(url);
        await ClipboardSetText(report);
      })
      .then(() => {
        setStatus('Opened — full report copied');
        window.setTimeout(() => {
          setStatus('');
        }, 2000);
      })
      .catch(() => {
        setStatus('Could not open issue');
      });
  }, [assemble, build]);

  return (
    <Button
      className="h-6 px-2 text-[11px] [&_svg]:size-3.5"
      type="button"
      variant="ghost"
      size="sm"
      onClick={reportIssue}
    >
      {status ? <CheckCircle2 aria-hidden="true" /> : <Bug aria-hidden="true" />}
      {status || 'Report an erun issue'}
    </Button>
  );
}
