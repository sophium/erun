import * as React from 'react';

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';

import { FieldLabel } from './FieldLabel';

export interface SelectFieldOption {
  value: string;
  label: string;
  disabled?: boolean;
}

export function SelectField({
  id,
  label,
  value,
  options,
  placeholder,
  emptyLabel,
  helper,
  disabled,
  required,
  onChange,
}: {
  id: string;
  label: string;
  value: string;
  options: SelectFieldOption[];
  placeholder?: string;
  emptyLabel?: string;
  helper?: string;
  disabled?: boolean;
  required?: boolean;
  onChange: (value: string) => void;
}): React.ReactElement {
  const noOptions = options.length === 0;
  const triggerDisabled = disabled === true || noOptions;
  const helperId = helper ? `${id}-helper` : undefined;
  return (
    <div className="grid min-w-0 gap-2">
      <FieldLabel htmlFor={id} required={required}>
        {label}
      </FieldLabel>
      <Select
        value={value || undefined}
        required={required}
        disabled={triggerDisabled}
        onValueChange={onChange}
      >
        <SelectTrigger
          id={id}
          className="w-full min-w-0 *:data-[slot=select-value]:min-w-0 *:data-[slot=select-value]:overflow-hidden *:data-[slot=select-value]:text-ellipsis"
          aria-describedby={helperId}
          aria-required={required}
        >
          <SelectValue
            placeholder={noOptions ? (emptyLabel ?? 'No options') : (placeholder ?? '')}
          />
        </SelectTrigger>
        {!noOptions && (
          // popper (not the default item-aligned) anchors the list to the trigger
          // and flips/clamps with collision detection, so it never renders off the
          // top of the dialog; collisionPadding keeps it clear of the window edges.
          <SelectContent position="popper" collisionPadding={12}>
            {options.map((item) => (
              <SelectItem key={item.value} value={item.value} disabled={item.disabled}>
                {item.label}
              </SelectItem>
            ))}
          </SelectContent>
        )}
      </Select>
      {helper && (
        <p id={helperId} className="text-[12px] leading-[1.4] text-muted-foreground">
          {helper}
        </p>
      )}
    </div>
  );
}
