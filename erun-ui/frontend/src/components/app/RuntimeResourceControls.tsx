import * as React from 'react';

import {
  clampRuntimePodConfig,
  formatNumber,
  MIN_RUNTIME_CPU_CORES,
  MIN_RUNTIME_MEMORY_GIB,
  parseDisplayNumber,
  RUNTIME_CPU_STEP,
  RUNTIME_MEMORY_STEP,
  runtimeResourceBounds,
  runtimeResourceValidation,
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
  // The create dialog blocks on an over-capacity request; the manage dialog,
  // where Save only persists config, surfaces it as a non-blocking warning.
  capacityBlocks?: boolean;
  onChange: (value: UIRuntimePodConfig) => void;
}

export function RuntimeResourceControls({
  idPrefix,
  value,
  status,
  loading,
  disabled,
  capacityBlocks = true,
  onChange,
}: RuntimeResourceControlsProps): React.ReactElement {
  const bounds = runtimeResourceBounds(status, loading);
  const { blockingError, capacityWarning } = runtimeResourceValidation(value, status);
  const controlsDisabled = disabled === true || loading || !bounds.available;
  const boundedValue = bounds.available ? clampRuntimePodConfig(value, bounds) : value;

  // The parent re-creates value and bounds every render, so the clamp effect
  // keys on primitive changes rather than the object identities.
  const onChangeRef = React.useRef(onChange);
  const valueRef = React.useRef(value);
  const boundsRef = React.useRef(bounds);
  React.useEffect(() => {
    onChangeRef.current = onChange;
    valueRef.current = value;
    boundsRef.current = bounds;
  });

  const { available: boundsAvailable, cpuMax, memoryMax } = bounds;
  const { cpu: currentCpu, memory: currentMemory } = value;
  React.useEffect(() => {
    if (!boundsAvailable) {
      return;
    }
    const latest = valueRef.current;
    const clamped = clampRuntimePodConfig(latest, boundsRef.current);
    if (clamped.cpu !== latest.cpu || clamped.memory !== latest.memory) {
      onChangeRef.current(clamped);
    }
  }, [boundsAvailable, cpuMax, memoryMax, currentCpu, currentMemory]);

  return (
    <div className="grid gap-3 rounded-[var(--radius)] border border-border p-3">
      <div className="grid gap-1">
        <div className="text-xs leading-[1.2] font-semibold tracking-normal text-muted-foreground uppercase">
          Runtime resources
        </div>
        {bounds.message && (
          <div
            className={
              !bounds.available && !bounds.loading
                ? 'text-xs leading-[1.35] text-destructive'
                : 'text-xs leading-[1.35] text-muted-foreground'
            }
          >
            {bounds.message}
          </div>
        )}
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
        onChange={(cpu) => {
          onChange({ ...value, cpu });
        }}
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
        onChange={(memory) => {
          onChange({ ...value, memory });
        }}
      />
      <RuntimeResourceMessages
        blockingError={blockingError}
        capacityWarning={capacityWarning}
        capacityBlocks={capacityBlocks}
      />
    </div>
  );
}

function RuntimeResourceMessages({
  blockingError,
  capacityWarning,
  capacityBlocks,
}: {
  blockingError: string;
  capacityWarning: string;
  capacityBlocks: boolean;
}): React.ReactElement | null {
  if (blockingError) {
    return (
      <div className="text-xs leading-[1.35] text-destructive" role="alert">
        {blockingError}
      </div>
    );
  }
  if (!capacityWarning) {
    return null;
  }
  return (
    <div
      className={
        capacityBlocks
          ? 'text-xs leading-[1.35] text-destructive'
          : 'text-xs leading-[1.35] text-amber-600 dark:text-amber-400'
      }
      role={capacityBlocks ? 'alert' : 'status'}
    >
      {capacityWarning}
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
            onChange={(event) => {
              onChange(event.target.value);
            }}
            onBlur={(event) => {
              onChange(
                formatNumber(clampToRange(parseDisplayNumber(event.target.value), min, max)),
              );
            }}
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
        onChange={(event) => {
          onChange(formatNumber(Number(event.target.value)));
        }}
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
