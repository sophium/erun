// Split from OperateToolForm.tsx for react-refresh/only-export-components.

export const OPERATE_TOOL_OPTIONS = [
  { value: 'deploy', label: 'Deploy — install a published version' },
  { value: 'context_start', label: 'Start the cloud context' },
  { value: 'context_stop', label: 'Stop the cloud context' },
  { value: 'resize', label: 'Resize the runtime pod' },
];

export interface OperateArgsInput {
  tool: string;
  version: string;
  contextName: string;
  force: boolean;
  cpu: string;
  memory: string;
  dindCpu: string;
  dindMemory: string;
  applyRecommendation: boolean;
  preview: boolean;
}

// Sends only the fields each tool's input schema declares.
export function buildOperateArgs(input: OperateArgsInput): Record<string, unknown> {
  const base: Record<string, unknown> = { preview: input.preview };
  switch (input.tool) {
    case 'deploy':
      return { ...base, version: input.version.trim() };
    case 'context_start':
      return { ...base, name: input.contextName.trim(), force: input.force };
    case 'context_stop':
      return { ...base, name: input.contextName.trim() };
    case 'resize':
      return {
        ...base,
        ...(input.cpu.trim() !== '' ? { cpu: input.cpu.trim() } : {}),
        ...(input.memory.trim() !== '' ? { memory: input.memory.trim() } : {}),
        ...(input.dindCpu.trim() !== '' ? { dindCpu: input.dindCpu.trim() } : {}),
        ...(input.dindMemory.trim() !== '' ? { dindMemory: input.dindMemory.trim() } : {}),
        applyRecommendation: input.applyRecommendation,
      };
    default:
      return base;
  }
}

// resize has no required field: an empty call is a no-op preview.
export function operateFormValid(input: {
  tool: string;
  version: string;
  contextName: string;
}): boolean {
  if (input.tool === 'deploy') {
    return input.version.trim() !== '';
  }
  if (input.tool === 'context_start' || input.tool === 'context_stop') {
    return input.contextName.trim() !== '';
  }
  return true;
}
