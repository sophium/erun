import * as React from 'react';

import {
  MIN_RUNTIME_CPU_CORES,
  MIN_RUNTIME_MEMORY_GIB,
  RUNTIME_CPU_STEP,
  RUNTIME_MEMORY_STEP,
  clampRuntimePodConfig,
  formatNumber,
  parseDisplayNumber,
  runtimeResourceBounds,
  runtimeResourceLimitMessage,
} from '@/app/runtimeResources';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import type { UIRuntimePodConfig, UIRuntimeResourceStatus } from '@/types';

interface RuntimeResourceControlsProps {
  idPrefix: string;
  value: UIRuntimePodConfig;
  status: UIRuntimeResourceStatus | null;
  loading: boolean;
  disabled?: boolean;
  onChange: (value: UIRuntimePodConfig) => void;
}

export function RuntimeResourceControls({ idPrefix, value, status, loading, disabled, onChange }: RuntimeResourceControlsProps): React.ReactElement {
  const bounds = runtimeResourceBounds(status, loading);
  const resourceError = runtimeResourceLimitMessage(value, status);
  const controlsDisabled = disabled || loading || !bounds.available;
  const boundedValue = bounds.available ? clampRuntimePodConfig(value, bounds) : value;

  React.useEffect(() => {
    if (!bounds.available) {
      return;
    }
    const clamped = clampRuntimePodConfig(value, bounds);
    if (clamped.cpu !== value.cpu || clamped.memory !== value.memory) {
      onChange(clamped);
    }
  }, [bounds.available, bounds.cpuMax, bounds.memoryMax, value.cpu, value.memory]);

  return (
    <div className="grid gap-3 rounded-[var(--radius)] border border-border p-3">
      <div className="grid gap-1">
        <div className="text-xs leading-[1.2] font-semibold tracking-normal text-muted-foreground uppercase">Runtime resources</div>
        {bounds.message && <div className={!bounds.available && !bounds.loading ? 'text-xs leading-[1.35] text-destructive' : 'text-xs leading-[1.35] text-muted-foreground'}>{bounds.message}</div>}
      </div>
      <ResourceControl
        id={`${idPrefix}-cpu`}
        label="CPU"
        unit="cores"
        min={MIN_RUNTIME_CPU_CORES}
        max={bounds.cpuMax}
        step={RUNTIME_CPU_STEP}
        value={boundedValue.cpu}
        disabled={controlsDisabled}
        onChange={(cpu) => onChange({ ...value, cpu })}
      />
      <ResourceControl
        id={`${idPrefix}-memory`}
        label="Memory"
        unit="GiB"
        min={MIN_RUNTIME_MEMORY_GIB}
        max={bounds.memoryMax}
        step={RUNTIME_MEMORY_STEP}
        value={boundedValue.memory}
        disabled={controlsDisabled}
        onChange={(memory) => onChange({ ...value, memory })}
      />
      {resourceError && <div className="text-xs leading-[1.35] text-destructive" role="alert">{resourceError}</div>}
    </div>
  );
}

function ResourceControl({
  id,
  label,
  unit,
  min,
  max,
  step,
  value,
  disabled,
  onChange,
}: {
  id: string;
  label: string;
  unit: string;
  min: number;
  max: number;
  step: number;
  value: string;
  disabled: boolean;
  onChange: (value: string) => void;
}): React.ReactElement {
  const numericValue = parseDisplayNumber(value);
  const sliderValue = Number.isFinite(numericValue) ? numericValue : min;
  const inputDisabled = disabled || max < min;
  return (
    <div className="grid gap-2">
      <div className="flex items-center justify-between gap-3">
        <Label htmlFor={`${id}-value`}>{label}</Label>
        <div className="flex items-center gap-2">
          <Input
            id={`${id}-value`}
            className="h-8 w-20 px-2 text-right"
            type="number"
            inputMode="decimal"
            min={min}
            max={max || undefined}
            step={step}
            value={value}
            disabled={inputDisabled}
            aria-describedby={`${id}-range`}
            onChange={(event) => onChange(event.target.value)}
            onBlur={(event) => onChange(formatNumber(clampToRange(parseDisplayNumber(event.target.value), min, max)))}
          />
          <span className="w-10 text-xs text-muted-foreground">{unit}</span>
        </div>
      </div>
      <input
        id={id}
        className="h-2 w-full accent-primary disabled:cursor-not-allowed disabled:opacity-50"
        type="range"
        min={min}
        max={Math.max(min, max)}
        step={step}
        value={clampToRange(sliderValue, min, Math.max(min, max))}
        disabled={inputDisabled}
        aria-label={`${label} ${unit}`}
        aria-describedby={`${id}-range`}
        onChange={(event) => onChange(formatNumber(Number(event.target.value)))}
      />
      <div id={`${id}-range`} className="flex justify-between text-xs text-muted-foreground">
        <span>Min {formatNumber(min)}</span>
        <span>Max {max >= min ? formatNumber(max) : 'unavailable'}</span>
      </div>
    </div>
  );
}

function clampToRange(value: number, min: number, max: number): number {
  if (!Number.isFinite(value)) {
    return min;
  }
  if (max < min) {
    return min;
  }
  return Math.min(max, Math.max(min, value));
}
