import * as React from 'react';
import { File, FileCode2, FileCog, FileJson, FileText, Gem } from 'lucide-react';

export function FileIcon({ filePath }: { filePath: string }): React.ReactElement {
  const Icon = fileIconForPath(filePath);
  return (
    <span className="file-icon" aria-hidden="true">
      <Icon className="file-icon-glyph" />
    </span>
  );
}

function fileIconForPath(filePath: string): typeof File {
  const name = filePath.split('/').pop()?.toLowerCase() || '';
  const extension = filePath.split('.').pop()?.toLowerCase() || '';
  if (['json', 'jsonc'].includes(extension)) {
    return FileJson;
  }
  if (extension === 'rb') {
    return Gem;
  }
  if (['yaml', 'yml', 'toml'].includes(extension) || name === 'dockerfile' || name === 'makefile') {
    return FileCog;
  }
  if (['md', 'mdx', 'txt'].includes(extension)) {
    return FileText;
  }
  if (['css', 'go', 'html', 'java', 'js', 'jsx', 'py', 'sh', 'ts', 'tsx'].includes(extension)) {
    return FileCode2;
  }
  return File;
}
