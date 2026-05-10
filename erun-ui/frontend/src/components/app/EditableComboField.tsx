import * as React from 'react';
import { Check, ChevronsUpDown } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { cn } from '@/lib/utils';

export function EditableComboField({
  id,
  inputRef,
  label,
  value,
  suggestions,
  required,
  disabled,
  onValueChange,
}: {
  id: string;
  inputRef?: React.Ref<HTMLInputElement>;
  label: string;
  value: string;
  suggestions: string[];
  required?: boolean;
  disabled?: boolean;
  onValueChange: (value: string) => void;
}): React.ReactElement {
  const [open, setOpen] = React.useState(false);
  const visibleSuggestions = React.useMemo(() => filterSuggestions(suggestions, value), [suggestions, value]);

  return (
    <div className="grid gap-2">
      <Label htmlFor={id}>{label}</Label>
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
          role="combobox"
          aria-expanded={open}
          aria-controls={`${id}-choices`}
          onChange={(event) => onValueChange(event.target.value)}
          onFocus={() => {
            if (!disabled && suggestions.length > 0) {
              setOpen(true);
            }
          }}
        />
        <Popover open={open} onOpenChange={setOpen}>
          <PopoverTrigger asChild>
            <Button
              className="absolute top-1 right-1 size-7 text-muted-foreground"
              type="button"
              variant="ghost"
              size="icon"
              aria-label={`Show ${label.toLowerCase()} choices`}
              disabled={disabled || suggestions.length === 0}
            >
              <ChevronsUpDown />
            </Button>
          </PopoverTrigger>
          <PopoverContent id={`${id}-choices`} className="w-96 max-w-[calc(100vw-4rem)] p-1" align="start">
            {visibleSuggestions.length === 0 ? (
              <div className="px-2 py-6 text-center text-sm text-muted-foreground">No matching values.</div>
            ) : (
              <div className="max-h-56 overflow-y-auto">
                {visibleSuggestions.map((suggestion) => {
                  const selected = suggestion === value;
                  return (
                    <button
                      key={suggestion}
                      className="flex min-h-8 w-full min-w-0 items-center gap-2 rounded-sm px-2 py-1.5 text-left text-sm outline-hidden hover:bg-accent hover:text-accent-foreground focus-visible:bg-accent focus-visible:text-accent-foreground"
                      type="button"
                      onClick={() => {
                        onValueChange(suggestion);
                        setOpen(false);
                      }}
                    >
                      <Check className={cn('size-4 shrink-0 opacity-0', selected && 'opacity-100')} />
                      <span className="truncate">{suggestion}</span>
                    </button>
                  );
                })}
              </div>
            )}
          </PopoverContent>
        </Popover>
      </div>
    </div>
  );
}

function filterSuggestions(suggestions: string[], value: string): string[] {
  const query = value.trim().toLowerCase();
  if (!query) {
    return suggestions;
  }
  return suggestions.filter((suggestion) => suggestion.toLowerCase().includes(query));
}

export function uniqueSuggestions(values: string[]): string[] {
  const result: string[] = [];
  const seen = new Set<string>();
  for (const value of values) {
    const normalized = value.trim();
    const key = normalized.toLowerCase();
    if (!normalized || seen.has(key)) {
      continue;
    }
    seen.add(key);
    result.push(normalized);
  }
  return result;
}
