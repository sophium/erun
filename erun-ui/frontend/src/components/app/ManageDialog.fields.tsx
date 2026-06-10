import * as React from 'react';

import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { cn } from '@/lib/utils';

export function ReadonlyField({
  id,
  label,
  value,
}: {
  id: string;
  label: string;
  value: string;
}): React.ReactElement {
  return (
    <div className="grid gap-2">
      <div id={id} className="text-sm font-medium leading-none">
        {label}
      </div>
      <div
        className="min-h-9 rounded-[var(--radius)] border border-border bg-muted/35 px-3 py-2 text-sm leading-[1.35] text-muted-foreground [overflow-wrap:anywhere]"
        aria-labelledby={id}
      >
        {value || 'Not configured'}
      </div>
    </div>
  );
}

export function CheckboxField({
  id,
  label,
  checked,
  helper,
  disabled,
  onChange,
}: {
  id: string;
  label: string;
  checked: boolean;
  helper?: string;
  disabled?: boolean;
  onChange: (checked: boolean) => void;
}): React.ReactElement {
  const helperId = helper ? `${id}-helper` : undefined;
  return (
    <div className="grid gap-2">
      <div className="flex items-center gap-2">
        <Checkbox
          id={id}
          checked={checked}
          disabled={disabled}
          aria-describedby={helperId}
          onCheckedChange={(value) => {
            onChange(value === true);
          }}
        />
        <Label htmlFor={id} className="text-sm font-normal">
          {label}
        </Label>
      </div>
      {helper && (
        <div id={helperId} className="text-[12px] leading-[1.4] text-muted-foreground">
          {helper}
        </div>
      )}
    </div>
  );
}

export function TextField({
  id,
  label,
  value,
  disabled,
  inputMode,
  inputRef,
  placeholder,
  helper,
  onChange,
}: {
  id: string;
  label: string;
  value: string;
  disabled?: boolean;
  inputMode?: React.HTMLAttributes<HTMLInputElement>['inputMode'];
  inputRef?: React.Ref<HTMLInputElement>;
  placeholder?: string;
  helper?: string;
  onChange: (value: string) => void;
}): React.ReactElement {
  const helperId = helper ? `${id}-helper` : undefined;
  return (
    <div className="grid gap-2">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        ref={inputRef}
        value={value}
        type="text"
        inputMode={inputMode}
        autoComplete="off"
        spellCheck={false}
        disabled={disabled}
        placeholder={placeholder}
        aria-describedby={helperId}
        onChange={(event) => {
          onChange(event.target.value);
        }}
      />
      {helper && (
        <div id={helperId} className="text-[12px] leading-[1.4] text-muted-foreground">
          {helper}
        </div>
      )}
    </div>
  );
}

export function StatusBadge({ status }: { status: string }): React.ReactElement {
  const normalized = status.trim() || 'unknown';
  const className =
    normalized === 'running'
      ? 'border-green-600/35 bg-green-600/10 text-green-700 dark:text-green-400'
      : normalized === 'stopped'
        ? 'border-border bg-muted/40 text-muted-foreground'
        : 'border-[color-mix(in_oklch,var(--destructive)_35%,var(--border))] bg-[color-mix(in_oklch,var(--destructive)_8%,transparent)] text-destructive';
  return (
    <span
      className={cn(
        'shrink-0 rounded-[calc(var(--radius)-2px)] border px-1.5 py-0.5 text-[11px] leading-none font-medium',
        className,
      )}
    >
      {normalized.replace(/_/g, ' ')}
    </span>
  );
}
