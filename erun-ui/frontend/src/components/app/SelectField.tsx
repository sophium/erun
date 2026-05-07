import * as React from 'react';

import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';

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
  disabled?: boolean;
  required?: boolean;
  onChange: (value: string) => void;
}): React.ReactElement {
  const noOptions = options.length === 0;
  const triggerDisabled = disabled || noOptions;
  return (
    <div className="grid gap-2">
      <Label htmlFor={id}>{label}</Label>
      <Select value={value || undefined} required={required} disabled={triggerDisabled} onValueChange={onChange}>
        <SelectTrigger id={id} className="w-full">
          <SelectValue placeholder={noOptions ? (emptyLabel ?? 'No options') : (placeholder ?? '')} />
        </SelectTrigger>
        {!noOptions && (
          <SelectContent>
            {options.map((item) => (
              <SelectItem key={item.value} value={item.value} disabled={item.disabled}>
                {item.label}
              </SelectItem>
            ))}
          </SelectContent>
        )}
      </Select>
    </div>
  );
}
