import { Button, Label } from 'erun-kit';
import { FolderPlus, X } from 'lucide-react';
import * as React from 'react';

import { ChooseLocalRepoPath } from '../../../wailsjs/go/main/App';

// DirectoriesField lists the directories the orchestrator operates in itself.
// They belong to no environment: no tenant, no pod, no cluster, no runtime
// version or image, and no role — none of those describe a path the orchestrator
// simply works in. Choosing one IS the whole interaction, which is why they are
// not rows in the environments list above: there is no environment to register
// and nothing to wire.
export function DirectoriesField({
  directories,
  disabled,
  onAdd,
  onRemove,
}: {
  directories: string[];
  disabled: boolean;
  onAdd: (directory: string) => void;
  onRemove: (directory: string) => void;
}): React.ReactElement {
  const choose = (): void => {
    void ChooseLocalRepoPath('').then((picked) => {
      const directory = picked.trim();
      if (directory !== '') {
        onAdd(directory);
      }
    });
  };
  return (
    <div className="space-y-1.5">
      <Label>Directories</Label>
      <p className="text-[12px] leading-[1.4] text-muted-foreground">
        Paths this orchestrator works in directly. They belong to no environment and nothing else
        owns them, so nothing is registered for one and no deploy applies.
      </p>
      {directories.length > 0 ? (
        <div className="space-y-2">
          {directories.map((directory) => (
            <div
              key={directory}
              className="flex items-center justify-between gap-2 rounded-[var(--radius)] border border-border px-3 py-2"
            >
              <span className="min-w-0 truncate font-mono text-xs">{directory}</span>
              <Button
                type="button"
                variant="ghost"
                size="icon-xs"
                disabled={disabled}
                aria-label={`Remove ${directory}`}
                onClick={() => {
                  onRemove(directory);
                }}
              >
                <X aria-hidden="true" />
              </Button>
            </div>
          ))}
        </div>
      ) : null}
      <Button type="button" variant="outline" size="sm" disabled={disabled} onClick={choose}>
        <FolderPlus aria-hidden="true" />
        Add directory…
      </Button>
    </div>
  );
}
