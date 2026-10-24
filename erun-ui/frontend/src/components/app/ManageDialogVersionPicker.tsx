import {
  Button,
  cn,
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  Input,
  Popover,
  PopoverContent,
  PopoverTrigger,
} from 'erun-kit';
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
import type { UIVersionSuggestion, UIVersionSuggestionNotice } from '@/types';

type ManageDialog = ManageDialogState;

// RuntimeDeployVersionPicker lists deployable versions by source line, with the component checklist.
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
  // Headings only with more than one source; a tenant env has two same-labelled lines.
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
          // Cap to the viewport height so the checklist and buttons stay on-screen.
          className="max-h-[var(--radix-popover-content-available-height)] w-[26rem] max-w-[calc(100vw-2rem)] overflow-y-auto p-0"
          align="start"
          // The dialog's focus scope refocuses its container on re-render, which Radix reads as a focus-out dismissal.
          // Pointer-outside, Escape and the trigger still close the panel.
          onFocusOutside={(event) => {
            event.preventDefault();
          }}
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
          <div className="px-3 pb-1">
            <RuntimeChartNotice dialog={dialog} id={RUNTIME_CHART_PANEL_NOTICE_ID} />
          </div>
          {/* The whole popover scrolls, so no nested scroll. */}
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
