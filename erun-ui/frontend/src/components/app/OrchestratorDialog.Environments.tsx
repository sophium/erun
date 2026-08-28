import { Button, Checkbox, Input, Label } from 'erun-kit';
import { Ban, FolderOpen } from 'lucide-react';
import * as React from 'react';

import type { OrchestratorEnvRef } from '@/app/slices/orchestratorsSlice';
import {
  type EnvCandidate,
  envKey,
} from '@/components/app/OrchestratorDialog.Environments.helpers';

import { ChooseLocalRepoPath } from '../../../wailsjs/go/main/App';

// EnvironmentsFieldStatus distinguishes the three states the field can be in,
// so "no environments configured" never reads the same as "you have several,
// none eligible" — the empty state itself must say which case applies and
// what to do next, rather than leaving an operator who knows an env exists to
// wonder whether erun even noticed it.
function EnvironmentsFieldStatus({
  totalCount,
  eligibleCount,
}: {
  totalCount: number;
  eligibleCount: number;
}): React.ReactElement | null {
  if (totalCount === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        No environments yet. Initialize one to orchestrate it.
      </p>
    );
  }
  if (eligibleCount === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        {totalCount} environment{totalCount === 1 ? '' : 's'} found, but none can be linked to an
        orchestrator — see the reason listed under each one below.
      </p>
    );
  }
  return null;
}

export function EnvironmentsField({
  candidates,
  selected,
  onToggle,
  onDirectoryChange,
}: {
  candidates: EnvCandidate[];
  selected: OrchestratorEnvRef[];
  onToggle: (candidate: EnvCandidate, checked: boolean) => void;
  onDirectoryChange: (ref: OrchestratorEnvRef, directory: string) => void;
}): React.ReactElement {
  const eligibleCount = candidates.filter((candidate) => candidate.eligible).length;
  return (
    <div className="space-y-1.5">
      <Label>Environments</Label>
      <EnvironmentsFieldStatus totalCount={candidates.length} eligibleCount={eligibleCount} />
      {candidates.length > 0 ? (
        <div className="max-h-56 space-y-2 overflow-y-auto">
          {candidates.map((candidate) => {
            const ref = selected.find(
              (entry) =>
                envKey(entry.tenant, entry.environment) ===
                envKey(candidate.tenant, candidate.environment),
            );
            return (
              <EnvironmentRow
                key={envKey(candidate.tenant, candidate.environment)}
                candidate={candidate}
                selectedRef={ref}
                onToggle={onToggle}
                onDirectoryChange={onDirectoryChange}
              />
            );
          })}
        </div>
      ) : null}
    </div>
  );
}

// IneligibleEnvironmentRow renders an env the picker considered and rejected:
// a disabled checkbox so it reads as unavailable rather than merely unchecked,
// and the reason as a first-class, always-visible line rather than a tooltip
// an operator has to go hunting for.
function IneligibleEnvironmentRow({ candidate }: { candidate: EnvCandidate }): React.ReactElement {
  return (
    <div className="rounded-sm border border-border/60 bg-muted/20 px-2 py-1.5">
      <label className="flex items-center gap-2 text-sm text-muted-foreground">
        <Checkbox
          checked={false}
          disabled
          aria-label={`${candidate.tenant} / ${candidate.environment} can't be linked`}
        />
        {candidate.tenant} / {candidate.environment}
      </label>
      <p className="mt-1 flex items-start gap-1.5 pl-6 text-xs text-muted-foreground">
        <Ban aria-hidden="true" className="mt-px size-3 shrink-0" />
        <span>{candidate.ineligibleReason}</span>
      </p>
    </div>
  );
}

function EnvironmentRow({
  candidate,
  selectedRef,
  onToggle,
  onDirectoryChange,
}: {
  candidate: EnvCandidate;
  selectedRef: OrchestratorEnvRef | undefined;
  onToggle: (candidate: EnvCandidate, checked: boolean) => void;
  onDirectoryChange: (ref: OrchestratorEnvRef, directory: string) => void;
}): React.ReactElement {
  if (!candidate.eligible) {
    return <IneligibleEnvironmentRow candidate={candidate} />;
  }
  const browse = (): void => {
    if (!selectedRef) {
      return;
    }
    void ChooseLocalRepoPath(selectedRef.directory).then((dir) => {
      if (dir.trim() !== '') {
        onDirectoryChange(selectedRef, dir.trim());
      }
    });
  };
  return (
    <div className="rounded-sm border border-border/60 px-2 py-1.5">
      <label className="flex items-center gap-2 text-sm">
        <Checkbox
          checked={Boolean(selectedRef)}
          onCheckedChange={(checked) => {
            onToggle(candidate, checked === true);
          }}
        />
        {candidate.tenant} / {candidate.environment}
        <span className="text-xs text-muted-foreground">
          {candidate.mirrored ? 'synced mirror' : 'worktree on this machine'}
        </span>
      </label>
      {selectedRef ? (
        <div className="mt-1.5 flex items-center gap-1.5 pl-6">
          {candidate.mirrored ? (
            <>
              <Input
                className="h-7 font-mono text-xs"
                value={selectedRef.directory}
                onChange={(event) => {
                  onDirectoryChange(selectedRef, event.target.value);
                }}
              />
              <Button
                type="button"
                variant="outline"
                size="icon-xs"
                className="size-7 flex-none"
                aria-label={`Choose sync directory for ${candidate.tenant} / ${candidate.environment}`}
                onClick={browse}
              >
                <FolderOpen aria-hidden="true" />
              </Button>
            </>
          ) : (
            // Derived from the env's repository path, so it is shown rather than
            // offered for editing: there is no mirror to place, and the operator
            // moves it by changing the env's repository path.
            <p className="min-w-0 truncate font-mono text-xs text-muted-foreground">
              {candidate.defaultDirectory}
            </p>
          )}
        </div>
      ) : null}
    </div>
  );
}
