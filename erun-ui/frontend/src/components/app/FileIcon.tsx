import * as React from 'react';
import { File, FileCode2, FileCog, FileJson, FileText, Gem } from 'lucide-react';

const extensionIcons = new Map([
  ['css', FileCode2],
  ['go', FileCode2],
  ['html', FileCode2],
  ['java', FileCode2],
  ['js', FileCode2],
  ['jsx', FileCode2],
  ['json', FileJson],
  ['jsonc', FileJson],
  ['md', FileText],
  ['mdx', FileText],
  ['py', FileCode2],
  ['rb', Gem],
  ['sh', FileCode2],
  ['toml', FileCog],
  ['ts', FileCode2],
  ['tsx', FileCode2],
  ['txt', FileText],
  ['yaml', FileCog],
  ['yml', FileCog],
]);

const filenameIcons = new Map([
  ['dockerfile', FileCog],
  ['makefile', FileCog],
]);

export function FileIcon({ filePath }: { filePath: string }): React.ReactElement {
  const Icon = fileIconForPath(filePath);
  return (
    <span
      className="inline-flex size-[22px] flex-none items-center justify-center rounded-[calc(var(--radius)-2px)] bg-muted text-[9px] leading-none font-bold text-muted-foreground"
      aria-hidden="true"
    >
      <Icon className="size-3.5" />
    </span>
  );
}

function fileIconForPath(filePath: string): typeof File {
  const name = filePath.split('/').pop()?.toLowerCase() || '';
  const extension = filePath.split('.').pop()?.toLowerCase() || '';
  return filenameIcons.get(name) ?? extensionIcons.get(extension) ?? File;
}
