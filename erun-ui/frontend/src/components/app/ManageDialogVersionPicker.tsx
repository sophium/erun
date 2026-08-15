import { Check, ChevronsUpDown } from 'lucide-react';
import * as React from 'react';

import { RUNTIME_CHART_PANEL_NOTICE_ID } from '@/app/runtimeChartPlan';
import type { ManageDialogState } from '@/app/state';
import {
  groupVersionSuggestionsBySource,
  versionChoiceImage,
  versionChoiceKind,
  versionChoiceLabel,
} from '@/app/versionSuggestions';
import { DeployComponentsField } from '@/components/app/ManageDialogDeployComponents';
import { RuntimeChartNotice } from '@/components/app/ManageDialogRuntimeChartNotice';
import { VersionNotices } from '@/components/app/VersionNotices';
import { Button } from '@/components/ui/button';
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command';
import { Input } from '@/components/ui/input';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { cn } from '@/lib/utils';
import type { UIVersionSuggestion, UIVersionSuggestionNotice } from '@/types';

type ManageDialog = ManageDialogState;

// The version picker: an environment's deployable versions, grouped by the line
// they come from, with that version's component checklist in the same panel.
// Extracted from ManageDialogRuntimeTab to keep both files inside the 500-line cap.
export function RuntimeDeployVersionPicker({
  dialog,
  overrideVersion,
  suggestions,
  notices,
  choicesOpen,
  disabled,
  onValueChange,
  onChoicesOpenChange,
  onSelect,
}: {
  dialog: ManageDialog;
  overrideVersion: string;
  suggestions: UIVersionSuggestion[];
  notices: UIVersionSuggestionNotice[];
  choicesOpen: boolean;
  disabled?: boolean;
  onValueChange: (version: string) => void;
  onChoicesOpenChange: (open: boolean) => void;
  onSelect: (suggestion: UIVersionSuggestion | undefined) => void;
}): React.ReactElement {
  // Group by source so a tenant env's two same-labelled lines — its own
  // <tenant>-devops runtime and the upstream erun-devops fallback — are told
  // apart. Headings only appear when there is more than one source; a single-line
  // picker keeps the plain "Version to deploy" heading.
  const suggestionGroups = groupVersionSuggestionsBySource(suggestions);
  const showSourceHeadings = suggestionGroups.length > 1;
  return (
    <div className="relative min-w-0">
      <Input
        id="manage-version"
        className="pr-10"
        value={overrideVersion}
        type="text"
        autoComplete="off"
        spellCheck={false}
        placeholder="Version to deploy"
        disabled={disabled}
        onChange={(event) => {
          onValueChange(event.target.value);
        }}
      />
      <Popover open={choicesOpen} onOpenChange={onChoicesOpenChange}>
        <PopoverTrigger asChild>
          <Button
            className="absolute right-1 top-1 size-7 text-muted-foreground"
            type="button"
            variant="ghost"
            size="icon"
            aria-label="Show version choices"
            disabled={disabled}
          >
            <ChevronsUpDown />
          </Button>
        </PopoverTrigger>
        <PopoverContent
          // Cap to the viewport's available height and scroll, so the version
          // list plus the components checklist never overflow off-screen (which
          // clipped the last components and the dialog buttons on shorter windows).
          className="max-h-[var(--radix-popover-content-available-height)] w-[26rem] max-w-[calc(100vw-2rem)] overflow-y-auto p-0"
          align="start"
        >
          <Command>
            <CommandInput placeholder="Search versions..." />
            <CommandList>
              <CommandEmpty>No version found.</CommandEmpty>
              {suggestionGroups.map((group) => (
                <CommandGroup
                  key={group.source || 'default'}
                  heading={showSourceHeadings ? group.heading : 'Version to deploy'}
                >
                  {group.suggestions.map((suggestion) => (
                    <RuntimeDeploySuggestionItem
                      key={`${suggestion.version}:${suggestion.image ?? ''}:${suggestion.source ?? ''}:${suggestion.label}`}
                      suggestion={suggestion}
                      selected={suggestion.version === overrideVersion}
                      onSelect={onSelect}
                    />
                  ))}
                </CommandGroup>
              ))}
            </CommandList>
          </Command>
          <VersionNotices notices={notices} />
          {/* The chart is the other half of what this version installs, so the
              panel says whether it exists before the operator picks. */}
          <div className="px-3 pb-1">
            <RuntimeChartNotice dialog={dialog} id={RUNTIME_CHART_PANEL_NOTICE_ID} />
          </div>
          {/* Pick a version above, then choose which of its charts to roll out:
              one panel so the component list always reads as that version's. The
              whole popover scrolls (capped to the viewport), so no nested scroll. */}
          <div className="border-t border-border p-3">
            <DeployComponentsField dialog={dialog} />
          </div>
        </PopoverContent>
      </Popover>
    </div>
  );
}

function RuntimeDeploySuggestionItem({
  suggestion,
  selected,
  onSelect,
}: {
  suggestion: UIVersionSuggestion;
  selected: boolean;
  onSelect: (suggestion: UIVersionSuggestion | undefined) => void;
}): React.ReactElement {
  return (
    <CommandItem
      className="min-w-0"
      value={versionChoiceLabel(suggestion)}
      onSelect={() => {
        onSelect(suggestion);
      }}
    >
      <Check className={cn('size-4 shrink-0 opacity-0', selected && 'opacity-100')} />
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="truncate text-sm font-medium leading-tight">{suggestion.version}</span>
        <span className="truncate text-xs leading-tight text-muted-foreground">
          {[versionChoiceImage(suggestion), versionChoiceKind(suggestion)]
            .filter(Boolean)
            .join(' | ')}
        </span>
      </span>
    </CommandItem>
  );
}
