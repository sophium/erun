import { Check, ChevronsUpDown } from 'lucide-react';
import * as React from 'react';

import {
  versionChoiceImage,
  versionChoiceKind,
  versionChoiceLabel,
} from '@/app/versionSuggestions';
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
import { Label } from '@/components/ui/label';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { cn } from '@/lib/utils';
import type { UIVersionSuggestion, UIVersionSuggestionNotice } from '@/types';

import { VersionNotices } from './VersionNotices';

export function VersionField({
  id,
  inputRef,
  value,
  sourceText,
  suggestions,
  notices,
  choicesOpen,
  required,
  disabled,
  onValueChange,
  onChoicesOpenChange,
  onSelect,
}: {
  id: string;
  inputRef?: React.Ref<HTMLInputElement>;
  value: string;
  sourceText: string;
  suggestions: UIVersionSuggestion[];
  notices: UIVersionSuggestionNotice[];
  choicesOpen: boolean;
  required?: boolean;
  disabled?: boolean;
  onValueChange: (version: string) => void;
  onChoicesOpenChange: (open: boolean) => void;
  onSelect: (suggestion: UIVersionSuggestion | undefined) => void;
}): React.ReactElement {
  return (
    <div className="grid gap-2">
      <Label htmlFor={id}>Runtime version</Label>
      <div className="relative">
        <Input
          id={id}
          ref={inputRef}
          className="pr-10"
          value={value}
          type="text"
          autoComplete="off"
          spellCheck={false}
          required={required}
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
          <PopoverContent className="w-80 p-0" align="start" collisionPadding={12}>
            <Command>
              <CommandInput placeholder="Search versions..." />
              <CommandList>
                <CommandEmpty>No version found.</CommandEmpty>
                <CommandGroup>
                  {suggestions.map((suggestion) => (
                    <VersionSuggestionItem
                      key={`${suggestion.version}:${suggestion.image ?? ''}:${suggestion.source ?? ''}:${suggestion.label}`}
                      suggestion={suggestion}
                      selected={suggestion.version === value}
                      onSelect={onSelect}
                    />
                  ))}
                </CommandGroup>
              </CommandList>
            </Command>
            <VersionNotices notices={notices} />
          </PopoverContent>
        </Popover>
      </div>
      <p className="min-h-4 text-xs text-muted-foreground">{sourceText}</p>
    </div>
  );
}

function VersionSuggestionItem({
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
