import type { UIRuntimePodConfig, UIRuntimeResourceStatus } from '@/types';

export const MIN_RUNTIME_CPU_CORES = 0.25;
export const MIN_RUNTIME_MEMORY_GIB = 1;
export const RUNTIME_CPU_STEP = 0.25;
export const RUNTIME_MEMORY_STEP = 0.5;

export interface RuntimeResourceBounds {
  cpuMax: number;
  memoryMax: number;
  loading: boolean;
  available: boolean;
  message: string;
}

export function runtimePodConfigToDisplay(config: UIRuntimePodConfig): UIRuntimePodConfig {
  return {
    cpu: formatNumber(parseCPUToCores(config.cpu) || 4),
    memory: formatNumber(parseMemoryToGiB(config.memory) || 8.7),
  };
}

export function runtimePodConfigToKubernetes(config: UIRuntimePodConfig): UIRuntimePodConfig {
  return {
    cpu: formatCPUQuantity(parseDisplayNumber(config.cpu)),
    memory: formatMemoryQuantity(parseDisplayNumber(config.memory)),
  };
}

export function runtimeResourceBounds(status: UIRuntimeResourceStatus | null, loading: boolean): RuntimeResourceBounds {
  if (loading) {
    return {
      cpuMax: 0,
      memoryMax: 0,
      loading: true,
      available: false,
      message: 'Checking capacity...',
    };
  }
  if (!status) {
    return {
      cpuMax: 0,
      memoryMax: 0,
      loading: false,
      available: false,
      message: '',
    };
  }
  if (!status.available) {
    return {
      cpuMax: 0,
      memoryMax: 0,
      loading: false,
      available: false,
      message: status.message || 'Capacity is unavailable.',
    };
  }
  return {
    cpuMax: roundToStep(status.cpu.free, RUNTIME_CPU_STEP),
    memoryMax: roundToStep(status.memory.free, RUNTIME_MEMORY_STEP),
    loading: false,
    available: true,
    message: status.message || `Available on best node: ${formatNumber(status.cpu.free)} CPU, ${formatNumber(status.memory.free)} GiB memory.`,
  };
}

export function runtimeResourceLimitMessage(config: UIRuntimePodConfig, status: UIRuntimeResourceStatus | null): string {
  const parsed = parseRuntimePodConfig(config);
  if (parsed.error) {
    if (status?.available && parsed.error.startsWith('CPU') && roundToStep(status.cpu.free, RUNTIME_CPU_STEP) < MIN_RUNTIME_CPU_CORES) {
      return 'No CPU capacity is available for this runtime.';
    }
    if (status?.available && parsed.error.startsWith('Memory') && roundToStep(status.memory.free, RUNTIME_MEMORY_STEP) < MIN_RUNTIME_MEMORY_GIB) {
      return 'No memory capacity is available for this runtime.';
    }
    return parsed.error;
  }
  if (!status?.available) {
    return '';
  }
  const matchingNode = (status.nodes || []).find((node) => parsed.cpuCores <= node.cpu.free && parsed.memoryGiB <= node.memory.free);
  if (status.nodes && status.nodes.length > 0 && !matchingNode) {
    return `No node has both ${formatNumber(parsed.cpuCores)} CPU and ${formatNumber(parsed.memoryGiB)} GiB memory available.`;
  }
  return '';
}

export function clampRuntimePodConfig(config: UIRuntimePodConfig, bounds: RuntimeResourceBounds): UIRuntimePodConfig {
  if (!bounds.available) {
    return config;
  }
  return {
    cpu: bounds.cpuMax >= MIN_RUNTIME_CPU_CORES ? formatNumber(clamp(parseDisplayNumber(config.cpu), MIN_RUNTIME_CPU_CORES, bounds.cpuMax)) : config.cpu,
    memory: bounds.memoryMax >= MIN_RUNTIME_MEMORY_GIB ? formatNumber(clamp(parseDisplayNumber(config.memory), MIN_RUNTIME_MEMORY_GIB, bounds.memoryMax)) : config.memory,
  };
}

export function parseDisplayNumber(value: string): number {
  return Number(value.trim().replace(',', '.'));
}

function parseRuntimePodConfig(config: UIRuntimePodConfig): { cpuCores: number; memoryGiB: number; error: string } {
  const cpuCores = parseDisplayNumber(config.cpu);
  if (!Number.isFinite(cpuCores) || cpuCores <= 0) {
    return { cpuCores: 0, memoryGiB: 0, error: 'CPU must be a positive number of cores.' };
  }
  const memoryGiB = parseDisplayNumber(config.memory);
  if (!Number.isFinite(memoryGiB) || memoryGiB <= 0) {
    return { cpuCores, memoryGiB: 0, error: 'Memory must be a positive GiB value.' };
  }
  return { cpuCores, memoryGiB, error: '' };
}

function parseCPUToCores(value: string): number {
  const trimmed = value.trim();
  if (trimmed.endsWith('m')) {
    return Number(trimmed.slice(0, -1)) / 1000;
  }
  return Number(trimmed);
}

function parseMemoryToGiB(value: string): number {
  const trimmed = value.trim();
  const units: Array<[string, number]> = [
    ['Ki', 1 / 1024 / 1024],
    ['Mi', 1 / 1024],
    ['Gi', 1],
    ['Ti', 1024],
    ['K', 1000 / 1024 / 1024 / 1024],
    ['M', 1000 * 1000 / 1024 / 1024 / 1024],
    ['G', 1000 * 1000 * 1000 / 1024 / 1024 / 1024],
    ['T', 1000 * 1000 * 1000 * 1000 / 1024 / 1024 / 1024],
  ];
  for (const [suffix, multiplier] of units) {
    if (trimmed.endsWith(suffix)) {
      return Number(trimmed.slice(0, -suffix.length)) * multiplier;
    }
  }
  return Number(trimmed);
}

function formatCPUQuantity(cores: number): string {
  if (!Number.isFinite(cores) || cores <= 0) {
    return '';
  }
  return formatNumber(cores);
}

function formatMemoryQuantity(gib: number): string {
  if (!Number.isFinite(gib) || gib <= 0) {
    return '';
  }
  return `${Math.round(gib * 1024)}Mi`;
}

function roundToStep(value: number, step: number): number {
  return Math.max(0, Math.floor(value / step) * step);
}

function clamp(value: number, min: number, max: number): number {
  if (!Number.isFinite(value)) {
    return min;
  }
  if (max < min) {
    return max;
  }
  return Math.min(max, Math.max(min, value));
}

export function formatNumber(value: number): string {
  if (!Number.isFinite(value)) {
    return '';
  }
  if (Number.isInteger(value)) {
    return String(value);
  }
  return value.toFixed(2).replace(/0+$/, '').replace(/\.$/, '');
}
