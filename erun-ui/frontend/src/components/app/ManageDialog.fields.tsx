import { Checkbox, Input, Label } from 'erun-kit';
import * as React from 'react';

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
