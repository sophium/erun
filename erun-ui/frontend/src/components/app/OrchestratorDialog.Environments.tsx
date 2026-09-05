import { Button, Checkbox, Input, Label, SelectField } from 'erun-kit';
import { Ban, FolderOpen } from 'lucide-react';
import * as React from 'react';

import type { OrchestratorEnvRef, OrchestratorEnvRole } from '@/app/slices/orchestratorsSlice';
import {
  type EnvCandidate,
  envKey,
  envRoleFieldId,
} from '@/components/app/OrchestratorDialog.Environments.helpers';

import { ChooseLocalRepoPath } from '../../../wailsjs/go/main/App';

// orchestratorEnvRoleHelper states, briefly, what the selected role means --
// where it is chosen, not only in the erun-orchestrate skill -- so an
// operator can pick between "code", "build", and "runtime" by what each does
// rather than by guessing from the name.
function orchestratorEnvRoleHelper(role: OrchestratorEnvRole): string {
  switch (role) {
    case 'code':
      return 'Writes code and iterates fast; not sized for a full regression run.';
    case 'build':
      return 'Checks out pushed branches, runs the gates, and cuts releases.';
    case 'runtime':
      return 'Operated directly — deploy, pin, observe — with no worktree to review and no in-pod agent to delegate to.';
    default:
      return 'No default is assumed until a role is picked.';
  }
}

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
  onRoleChange,
}: {
  candidates: EnvCandidate[];
  selected: OrchestratorEnvRef[];
  onToggle: (candidate: EnvCandidate, checked: boolean) => void;
  onDirectoryChange: (ref: OrchestratorEnvRef, directory: string) => void;
  onRoleChange: (ref: OrchestratorEnvRef, role: OrchestratorEnvRole) => void;
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
                onRoleChange={onRoleChange}
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

// EnvironmentRowDirectory renders the review-directory controls for a
// selected, non-required-role candidate: an editable mirror path (with a
// browse button) or the derived worktree path, shown read-only. Split out of
// EnvironmentRow to keep that function within the lint size budget.
function EnvironmentRowDirectory({
  candidate,
  selectedRef,
  onDirectoryChange,
}: {
  candidate: EnvCandidate;
  selectedRef: OrchestratorEnvRef;
  onDirectoryChange: (ref: OrchestratorEnvRef, directory: string) => void;
}): React.ReactElement {
  const browse = (): void => {
    void ChooseLocalRepoPath(selectedRef.directory).then((dir) => {
      if (dir.trim() !== '') {
        onDirectoryChange(selectedRef, dir.trim());
      }
    });
  };
  if (!candidate.mirrored) {
    // Derived from the env's repository path, so it is shown rather than
    // offered for editing: there is no mirror to place, and the operator
    // moves it by changing the env's repository path.
    return (
      <p className="min-w-0 truncate font-mono text-xs text-muted-foreground">
        {candidate.defaultDirectory}
      </p>
    );
  }
  return (
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
  );
}

// EnvironmentRowRole renders the role control for a selected candidate: a
// real Select when more than one role is legal, or a plain statement of fact
// when a requiredRole leaves only one -- a single-option Select would offer
// a choice that isn't one. Split out of EnvironmentRow for the same reason
// as EnvironmentRowDirectory above.
function EnvironmentRowRole({
  candidate,
  selectedRef,
  onRoleChange,
}: {
  candidate: EnvCandidate;
  selectedRef: OrchestratorEnvRef;
  onRoleChange: (ref: OrchestratorEnvRef, role: OrchestratorEnvRole) => void;
}): React.ReactElement {
  if (candidate.requiredRole) {
    return (
      <p className="mt-1.5 pl-6 text-xs text-muted-foreground">
        Role: Runtime — {orchestratorEnvRoleHelper(candidate.requiredRole)}
      </p>
    );
  }
  return (
    <div className="mt-1.5 pl-6">
      <SelectField
        id={envRoleFieldId(candidate.tenant, candidate.environment)}
        label="Role"
        // Radix's Select.Item rejects an empty-string value, so undeclared
        // (OrchestratorEnvRole's own '') is represented here as the
        // sentinel "none" and translated back at the boundary -- the
        // option list and the round-trip below are the only two places
        // that need to know about it.
        value={selectedRef.role === '' ? 'none' : selectedRef.role}
        options={[
          { value: 'none', label: 'Not declared' },
          { value: 'code', label: 'Code' },
          { value: 'build', label: 'Build' },
        ]}
        helper={orchestratorEnvRoleHelper(selectedRef.role)}
        onChange={(value) => {
          onRoleChange(selectedRef, (value === 'none' ? '' : value) as OrchestratorEnvRole);
        }}
      />
    </div>
  );
}

function EnvironmentRow({
  candidate,
  selectedRef,
  onToggle,
  onDirectoryChange,
  onRoleChange,
}: {
  candidate: EnvCandidate;
  selectedRef: OrchestratorEnvRef | undefined;
  onToggle: (candidate: EnvCandidate, checked: boolean) => void;
  onDirectoryChange: (ref: OrchestratorEnvRef, directory: string) => void;
  onRoleChange: (ref: OrchestratorEnvRef, role: OrchestratorEnvRole) => void;
}): React.ReactElement {
  if (!candidate.eligible) {
    return <IneligibleEnvironmentRow candidate={candidate} />;
  }
  // A required-role candidate (today, only a runtime environment) has no
  // review directory at all -- EnvironmentRowDirectory below is skipped
  // entirely for it rather than rendering a picker with nothing to show.
  const requiredRole = candidate.requiredRole;
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
          {requiredRole
            ? 'operated directly — no review directory'
            : candidate.mirrored
              ? 'synced mirror'
              : 'worktree on this machine'}
        </span>
      </label>
      {selectedRef && !requiredRole ? (
        <div className="mt-1.5 flex items-center gap-1.5 pl-6">
          <EnvironmentRowDirectory
            candidate={candidate}
            selectedRef={selectedRef}
            onDirectoryChange={onDirectoryChange}
          />
        </div>
      ) : null}
      {selectedRef ? (
        <EnvironmentRowRole
          candidate={candidate}
          selectedRef={selectedRef}
          onRoleChange={onRoleChange}
        />
      ) : null}
    </div>
  );
}
